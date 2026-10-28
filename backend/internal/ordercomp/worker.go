// Package ordercomp applies durable order events to Redis: it restores inventory
// and releases purchase eligibility exactly once per event id. MySQL is the fact
// source; Redis is a projection that can be rebuilt.
package ordercomp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/outbox"
)

//go:embed lua/restore.lua
var restoreBody string

var restore = redis.NewScript(restoreBody)

const (
	applied   = 1
	duplicate = 0
	badState  = -2
	badBound  = -3
)

var (
	errPayload = errors.New("order event payload is not usable")
	errMissing = errors.New("activity no longer exists for this compensation")
)

type Worker struct {
	db        *gorm.DB
	redis     *redis.Client
	repo      *outbox.Repository
	lastEvent string
}

func NewWorker(db *gorm.DB, client *redis.Client) *Worker {
	return &Worker{db: db, redis: client, repo: outbox.NewRepository(db)}
}

// Run polls until the process context is cancelled. A transient database or Redis
// error leaves the event PENDING with a bounded backoff, never deleted.
func (worker *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := worker.Poll(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// Driver text can contain request contents; the durable row is the
			// diagnostic source instead of free-form exception logging.
			slog.Warn("order compensation remains pending", "event_id", worker.lastEvent)
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type payload struct {
	VoucherID          string `json:"voucherId"`
	UserID             string `json:"userId"`
	OrderID            string `json:"orderId"`
	StockRestore       string `json:"stockRestore"`
	ReleaseEligibility string `json:"releaseEligibility"`
}

func (worker *Worker) Poll(ctx context.Context) (bool, error) {
	event, err := worker.repo.ClaimOrder(ctx)
	if err != nil {
		return false, err
	}
	if event == nil {
		return false, nil
	}
	worker.lastEvent = event.EventID
	return true, worker.Apply(ctx, *event)
}

// Apply is idempotent per event id. A payment event carries stockRestore=0 and
// only marks the outbox row done; cancel/refund events restore one unit.
func (worker *Worker) Apply(ctx context.Context, event outbox.Event) error {
	parsed, err := parseEvent(event)
	if err != nil {
		return worker.repo.NeedsReview(ctx, event, err)
	}
	if parsed.StockRestore == "0" {
		return worker.repo.Done(ctx, event)
	}
	if event.AggregateType == "RESERVATION" {
		var exists bool
		if err := worker.db.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM tb_voucher_order WHERE id=?)", parsed.OrderID).Scan(&exists).Error; err != nil {
			return worker.repo.Retry(ctx, event, err)
		}
		if exists {
			return worker.repo.NeedsReview(ctx, event, errors.New("rejected reservation has a committed order"))
		}
	}
	maxStock, err := worker.totalStock(ctx, parsed.VoucherID)
	if err != nil {
		if errors.Is(err, errMissing) {
			return worker.repo.NeedsReview(ctx, event, err)
		}
		return worker.repo.Retry(ctx, event, err)
	}
	release := "0"
	if parsed.ReleaseEligibility == "true" {
		release = "1"
	}
	keys := []string{
		"comp:done:" + event.EventID,
		"seckill:stock:" + parsed.VoucherID,
		"seckill:order:" + parsed.VoucherID,
		"seckill:accepted:" + parsed.VoucherID,
	}
	result, err := restore.Run(ctx, worker.redis, keys, parsed.UserID, parsed.OrderID, release, maxStock).Int()
	if err != nil {
		// A Lua error that is not one of the documented codes is not assumed to
		// have rolled back; keep the event retryable and visible.
		return worker.repo.Retry(ctx, event, err)
	}
	switch classify(result) {
	case finishApplied:
		return worker.repo.Done(ctx, event)
	case needsReconcile:
		return worker.repo.NeedsReview(ctx, event, errors.New(reconcileReason(result)))
	default:
		return worker.repo.NeedsReview(ctx, event,
			fmt.Errorf("unexpected restore result %d", result))
	}
}

type outcome int

const (
	finishApplied outcome = iota
	needsReconcile
	unknownResult
)

// classify maps the script result codes to a delivery decision.
func classify(result int) outcome {
	switch result {
	case applied, duplicate:
		return finishApplied
	case badState, badBound:
		return needsReconcile
	default:
		return unknownResult
	}
}

func reconcileReason(result int) string {
	if result == badBound {
		return "restoring stock would exceed total_stock"
	}
	return "stock key missing, wrong type or not numeric"
}

func (worker *Worker) totalStock(ctx context.Context, voucherID string) (string, error) {
	var total *uint64
	if err := worker.db.WithContext(ctx).Raw(
		"SELECT total_stock FROM tb_seckill_voucher WHERE voucher_id=?", voucherID).Scan(&total).Error; err != nil {
		return "", err
	}
	if total == nil {
		return "", errMissing
	}
	return strconv.FormatUint(*total, 10), nil
}

func parse(raw json.RawMessage) (payload, error) {
	var parsed payload
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return payload{}, errPayload
	}
	for _, id := range []string{parsed.VoucherID, parsed.UserID, parsed.OrderID} {
		if !canonical(id) {
			return payload{}, errPayload
		}
	}
	if parsed.StockRestore != "0" && parsed.StockRestore != "1" {
		return payload{}, errPayload
	}
	if parsed.ReleaseEligibility != "true" && parsed.ReleaseEligibility != "false" {
		return payload{}, errPayload
	}
	return parsed, nil
}

func parseEvent(event outbox.Event) (payload, error) {
	if event.AggregateType == "ORDER" && event.EventType == "ORDER_CHANGED" {
		p, err := parse(event.Payload)
		if err != nil || p.OrderID != event.AggregateID {
			return payload{}, errPayload
		}
		return p, nil
	}
	if event.AggregateType != "RESERVATION" || event.EventType != "RESERVATION_REJECTED" {
		return payload{}, errPayload
	}
	var rejection struct {
		OrderID         string `json:"orderId"`
		UserID          string `json:"userId"`
		VoucherID       string `json:"voucherId"`
		StockDelta      int    `json:"stockDelta"`
		ReleasePurchase bool   `json:"releasePurchase"`
		Reason          string `json:"reason"`
	}
	if json.Unmarshal(event.Payload, &rejection) != nil || rejection.OrderID != event.AggregateID || event.EventID != "RESERVED:"+event.AggregateID+":REJECTED" || rejection.StockDelta != 1 || rejection.Reason == "" {
		return payload{}, errPayload
	}
	for _, id := range []string{rejection.OrderID, rejection.UserID, rejection.VoucherID} {
		if !canonical(id) {
			return payload{}, errPayload
		}
	}
	p := payload{OrderID: rejection.OrderID, UserID: rejection.UserID, VoucherID: rejection.VoucherID, StockRestore: "1", ReleaseEligibility: "false"}
	if rejection.ReleasePurchase {
		p.ReleaseEligibility = "true"
	}
	return p, nil
}

func canonical(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == value
}
