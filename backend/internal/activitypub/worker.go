package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/outbox"
)

// Worker owns no HTTP context. Run returns once the application context is
// cancelled and its current bounded delivery has released its transaction.
type Worker struct {
	db     *gorm.DB
	gate   *Gate
	outbox *outbox.Repository
}

func NewWorker(db *gorm.DB, client *redis.Client) *Worker {
	return &Worker{db: db, gate: NewGate(client), outbox: outbox.NewRepository(db)}
}

func (worker *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := worker.Poll(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("activity outbox delivery will retry", "error", err)
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

// Poll processes at most one event so no claimed event waits behind a batch
// until its lease has expired. Every attempt has a five-second deadline.
func (worker *Worker) Poll(ctx context.Context) (bool, error) {
	claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	event, err := worker.outbox.ClaimActivity(claimCtx)
	cancel()
	if err != nil || event == nil {
		return false, err
	}
	attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = worker.dispatch(attemptCtx, *event)
	cancel()
	// Cancellation leaves a recoverable PROCESSING lease, never a false DONE.
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	finishCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err != nil {
		if retryErr := worker.outbox.Retry(finishCtx, *event, err); retryErr != nil {
			return true, errors.Join(err, retryErr)
		}
		return true, err
	}
	return true, worker.outbox.Done(finishCtx, *event)
}

type publicationRow struct {
	VoucherID      uint64
	MerchantID     uint64
	ShopID         uint64
	PublishStatus  string
	PublishVersion int
	Stock          int
	BeginTime      time.Time
	EndTime        time.Time
}

func (worker *Worker) dispatch(ctx context.Context, event outbox.Event) error {
	if event.AggregateType != "ACTIVITY" || !validID(event.AggregateID) ||
		event.AggregateVersion < 0 || event.AggregateVersion > 2147483647 {
		return ErrUnsafe
	}
	if event.EventType != "ACTIVITY_PREPARE" && event.EventType != "ACTIVITY_OPEN" && event.EventType != "ACTIVITY_CLOSE" {
		return ErrUnsafe
	}
	var payload struct {
		ActivityID string `json:"activityId"`
		Version    *int   `json:"version"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil ||
		(payload.ActivityID != "" && payload.ActivityID != event.AggregateID) ||
		(payload.Version != nil && *payload.Version != event.AggregateVersion) {
		return ErrUnsafe
	}
	return worker.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// API mutations lock voucher before inventory. Preserve that ordering
		// here so an OPEN cannot deadlock an offline request holding voucher.
		var vouchers []struct{ ShopID uint64 }
		if err := tx.Raw("SELECT shop_id FROM tb_voucher WHERE id = ? FOR SHARE", event.AggregateID).Scan(&vouchers).Error; err != nil {
			return err
		}
		if len(vouchers) != 1 {
			return errors.New("activity publication voucher is missing")
		}
		var rows []publicationRow
		// All publication/offline paths serialize on this current locking read.
		// No ordinary RR snapshot is consulted to decide whether to open.
		if err := tx.Raw(`SELECT voucher_id, merchant_id,
			publish_status, publish_version, stock, begin_time, end_time
			FROM tb_seckill_voucher WHERE voucher_id = ? FOR UPDATE`, event.AggregateID).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) != 1 {
			return errors.New("activity publication resource is missing")
		}
		row := rows[0]
		row.ShopID = vouchers[0].ShopID
		switch event.EventType {
		case "ACTIVITY_PREPARE":
			if row.PublishStatus == "OFFLINE" || row.PublishStatus == "ACTIVE" {
				return nil
			}
			if row.PublishStatus != "INITIALIZING" || row.PublishVersion != event.AggregateVersion || row.PublishVersion >= 2147483647 {
				return ErrUnsafe
			}
			target := row.PublishVersion + 1
			if err := worker.gate.Prepare(ctx, event.AggregateID, target, row.Stock, row.BeginTime, row.EndTime); err != nil {
				return err
			}
			changed := tx.Exec(`UPDATE tb_seckill_voucher SET publish_status = 'ACTIVE', publish_version = ?
				WHERE voucher_id = ? AND publish_status = 'INITIALIZING' AND publish_version = ?`, target, row.VoucherID, row.PublishVersion)
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected != 1 {
				return errors.New("activity changed during preparation")
			}
			detail, _ := json.Marshal(map[string]any{"version": target})
			if err := tx.Exec(`INSERT INTO tb_audit_log
				(merchant_id, shop_id, operator_user_id, operator_type, action, resource_id, detail)
				VALUES (?, ?, 0, 'SYSTEM', 'ACTIVITY_ACTIVE', ?, ?)`,
				row.MerchantID, row.ShopID, event.AggregateID, string(detail)).Error; err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]any{"activityId": event.AggregateID, "version": target})
			// Gate remains PREPARED/CLOSED through commit. Only the separately
			// claimed committed OPEN event below can start admission.
			return outbox.Enqueue(ctx, tx, outbox.Event{
				EventID:       fmt.Sprintf("ACTIVITY:%s:%d:OPEN", event.AggregateID, target),
				AggregateType: "ACTIVITY", AggregateID: event.AggregateID,
				AggregateVersion: target, EventType: "ACTIVITY_OPEN", Payload: payload,
			})
		case "ACTIVITY_OPEN":
			if row.PublishStatus != "ACTIVE" || row.PublishVersion != event.AggregateVersion {
				return nil
			}
			err := worker.gate.Open(ctx, event.AggregateID, event.AggregateVersion)
			if errors.Is(err, ErrStale) {
				return nil
			}
			return err
		case "ACTIVITY_CLOSE":
			if row.PublishStatus != "OFFLINE" || row.PublishVersion != event.AggregateVersion {
				return nil
			}
			err := worker.gate.Close(ctx, event.AggregateID, event.AggregateVersion)
			if errors.Is(err, ErrStale) {
				return nil
			}
			return err
		}
		return ErrUnsafe
	})
}
