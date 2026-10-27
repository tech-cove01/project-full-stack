package order

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/outbox"
)

type rejection struct {
	OrderID         string `json:"orderId"`
	UserID          string `json:"userId"`
	VoucherID       string `json:"voucherId"`
	Reason          string `json:"reason"`
	StockDelta      int    `json:"stockDelta"`
	ReleasePurchase bool   `json:"releasePurchase"`
}

func rejectionID(id string) string { return "RESERVED:" + id + ":REJECTED" }

func findOrder(ctx context.Context, db *gorm.DB, id int64, lock bool) (*record, error) {
	query := "SELECT " + columns + " FROM tb_voucher_order WHERE id=?"
	if lock {
		query += " FOR UPDATE"
	}
	var row record
	if err := db.WithContext(ctx).Raw(query, id).Scan(&row).Error; err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

func findRejection(ctx context.Context, db *gorm.DB, id string) (*rejection, error) {
	var event outbox.Event
	if err := db.WithContext(ctx).Raw(`SELECT id,event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload
 FROM tb_outbox WHERE event_id=?`, rejectionID(id)).Scan(&event).Error; err != nil {
		return nil, err
	}
	if event.ID == 0 {
		return nil, nil
	}
	if event.AggregateType != "RESERVATION" || event.AggregateID != id || event.AggregateVersion != 0 || event.EventType != "RESERVATION_REJECTED" {
		return nil, ErrUnsafe
	}
	var rejected rejection
	if json.Unmarshal(event.Payload, &rejected) != nil || rejected.OrderID != id || rejected.Reason == "" || rejected.StockDelta != 1 {
		return nil, ErrUnsafe
	}
	if _, err := unsignedID(rejected.UserID); err != nil {
		return nil, ErrUnsafe
	}
	if _, err := unsignedID(rejected.VoucherID); err != nil {
		return nil, ErrUnsafe
	}
	return &rejected, nil
}

func existingOutcome(ctx context.Context, tx *gorm.DB, id int64, user, voucher uint64) (*Outcome, error) {
	existing, err := findOrder(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.UserID != user || existing.VoucherID != voucher {
			return nil, ErrCollision
		}
		view := toView(*existing, false)
		return &Outcome{State: Created, View: &view}, nil
	}
	rejected, err := findRejection(ctx, tx, strconv.FormatInt(id, 10))
	if err != nil {
		return nil, err
	}
	if rejected != nil {
		if rejected.UserID != decimal(user) || rejected.VoucherID != decimal(voucher) {
			return nil, ErrCollision
		}
		view := View{ID: rejected.OrderID, State: Failed, Message: rejected.Reason}
		return &Outcome{State: Failed, View: &view}, nil
	}
	return nil, nil
}

// Create returns a terminal state only after Transaction has committed. A
// duplicate insert is reconciled in a fresh transaction after the failed stock
// change rolls back; arbitrary 1062 errors are never labeled as limit failures.
func (creator *Creator) Create(ctx context.Context, id, userID, voucherID string) (Outcome, error) {
	order, err := orderID(id)
	if err != nil {
		return Outcome{}, err
	}
	user, err := unsignedID(userID)
	if err != nil {
		return Outcome{}, err
	}
	voucher, err := unsignedID(voucherID)
	if err != nil {
		return Outcome{}, err
	}
	if creator.payTimeout <= 0 || creator.payTimeout%time.Second != 0 {
		return Outcome{}, ErrUnsafe
	}
	var outcome Outcome
	err = creator.db.WithContext(ctx).Connection(func(conn *gorm.DB) (returnErr error) {
		// Order and rejection facts live in separate tables. A connection-owned
		// lock serializes this identifier across both outcomes and across voucher
		// rows without introducing a second source of durable order identities.
		name := "go:order:" + id
		var locked struct{ Acquired *int }
		if err := conn.Raw("SELECT GET_LOCK(?,0) AS acquired", name).Scan(&locked).Error; err != nil {
			// The server may have acquired the lock before a response was lost.
			discardConnection(conn)
			return err
		}
		if locked.Acquired == nil || *locked.Acquired != 1 {
			return ErrUnsafe
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var result struct{ Released *int }
			err := conn.WithContext(releaseCtx).Raw("SELECT RELEASE_LOCK(?) AS released", name).Scan(&result).Error
			if err == nil && result.Released != nil && *result.Released == 1 {
				return
			}
			// A failed release must not put a session still holding GET_LOCK back
			// into the pool. ErrBadConn discards the physical MySQL connection.
			discardConnection(conn)
			returnErr = errors.Join(returnErr, ErrUnsafe)
		}()
		outcome, returnErr = creator.createLocked(ctx, conn, order, user, voucher, id, userID, voucherID)
		return returnErr
	})
	if err != nil {
		return Outcome{}, err
	}
	return outcome, nil
}

func discardConnection(conn *gorm.DB) {
	if physical, ok := conn.Statement.ConnPool.(*sql.Conn); ok {
		_ = physical.Raw(func(any) error { return driver.ErrBadConn })
	}
}

func (creator *Creator) createLocked(ctx context.Context, conn *gorm.DB, order int64, user, voucher uint64, id, userID, voucherID string) (Outcome, error) {
	var outcome Outcome
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			known, err := existingOutcome(ctx, tx, order, user, voucher)
			if err != nil {
				return err
			}
			if known != nil {
				outcome = *known
				return nil
			}
			var catalog struct {
				ID       uint64
				ShopID   uint64
				PayValue uint64
			}
			if err = tx.Raw("SELECT id,shop_id,pay_value FROM tb_voucher WHERE id=? FOR SHARE", voucher).Scan(&catalog).Error; err != nil {
				return err
			}
			if catalog.ID == 0 || catalog.ShopID == 0 {
				return ErrUnsafe
			}
			var activity struct {
				VoucherID       uint64
				MerchantID      uint64
				Stock           int
				RedeemBeginTime *time.Time
				RedeemEndTime   *time.Time
			}
			if err = tx.Raw(`SELECT voucher_id,merchant_id,stock,redeem_begin_time,redeem_end_time
 FROM tb_seckill_voucher WHERE voucher_id=? FOR UPDATE`, voucher).Scan(&activity).Error; err != nil {
				return err
			}
			if activity.VoucherID == 0 || activity.MerchantID == 0 {
				return ErrUnsafe
			}
			// Another same-voucher creation may have committed while acquiring
			// inventory. Recheck using current reads before a second deduction.
			known, err = existingOutcome(ctx, tx, order, user, voucher)
			if err != nil {
				return err
			}
			if known != nil {
				outcome = *known
				return nil
			}
			var active struct{ ID int64 }
			if err = tx.Raw(`SELECT id FROM tb_voucher_order WHERE user_id=? AND voucher_id=?
 AND active_flag=1 LIMIT 1 FOR UPDATE`, user, voucher).Scan(&active).Error; err != nil {
				return err
			}
			if active.ID != 0 {
				return creator.reject(ctx, tx, id, userID, voucherID, "该活动已有有效订单", false, &outcome)
			}
			if activity.Stock <= 0 {
				return creator.reject(ctx, tx, id, userID, voucherID, "活动库存不足", true, &outcome)
			}
			var clock struct{ Value time.Time }
			if err = tx.Raw("SELECT NOW() AS value").Scan(&clock).Error; err != nil {
				return err
			}
			if clock.Value.IsZero() {
				return ErrUnsafe
			}
			deadline := clock.Value.Add(creator.payTimeout)
			if activity.RedeemEndTime != nil && deadline.After(*activity.RedeemEndTime) {
				deadline = *activity.RedeemEndTime
			}
			if deadline.Year() < 1000 || deadline.Year() > 9999 {
				return ErrUnsafe
			}
			changed := tx.Exec("UPDATE tb_seckill_voucher SET stock=stock-1 WHERE voucher_id=? AND stock>0", voucher)
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected != 1 {
				return ErrUnsafe
			}
			row := record{ID: order, UserID: user, VoucherID: voucher, ShopID: catalog.ShopID, PayType: 1, PayAmount: catalog.PayValue,
				Status: 1, Version: 0, PayDeadlineAt: deadline, RedeemBeginTime: activity.RedeemBeginTime, RedeemEndTime: activity.RedeemEndTime, CreateTime: clock.Value}
			if err = tx.Create(&row).Error; err != nil {
				return err
			}
			detail, _ := json.Marshal(map[string]any{"orderStatus": 1, "version": 0})
			if err = tx.Exec(`INSERT INTO tb_audit_log(merchant_id,shop_id,operator_user_id,operator_type,action,resource_id,detail)
 VALUES(?,?,?,'USER','ORDER_CREATE',?,?)`, activity.MerchantID, catalog.ShopID, user, id, string(detail)).Error; err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]string{"orderId": id, "voucherId": voucherID, "shopId": decimal(catalog.ShopID), "userId": userID,
				"targetStatus": "1", "stockRestore": "0", "releaseEligibility": "false", "version": "0"})
			if err = outbox.Enqueue(ctx, tx, outbox.Event{EventID: "ORDER:" + id + ":0:CHANGED", AggregateType: "ORDER", AggregateID: id,
				AggregateVersion: 0, EventType: "ORDER_CHANGED", Payload: payload}); err != nil {
				return err
			}
			view := toView(row, false)
			outcome = Outcome{State: Created, View: &view}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err == nil {
			return outcome, nil
		}
		var duplicate *mysqldriver.MySQLError
		if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
			return Outcome{}, err
		}
	}
	return Outcome{}, err
}

func (creator *Creator) reject(ctx context.Context, tx *gorm.DB, id, user, voucher, reason string, release bool, outcome *Outcome) error {
	payload, err := json.Marshal(rejection{OrderID: id, UserID: user, VoucherID: voucher, Reason: reason, StockDelta: 1, ReleasePurchase: release})
	if err != nil {
		return err
	}
	if err = outbox.Enqueue(ctx, tx, outbox.Event{EventID: rejectionID(id), AggregateType: "RESERVATION", AggregateID: id, AggregateVersion: 0,
		EventType: "RESERVATION_REJECTED", Payload: payload}); err != nil {
		return err
	}
	view := View{ID: id, State: Failed, Message: reason}
	*outcome = Outcome{State: Failed, View: &view}
	return nil
}
