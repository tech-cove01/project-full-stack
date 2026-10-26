package ordertimeout

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/order"
	"github.com/cityloop/fullstack-go/internal/outbox"
)

// Unlike integration_test.go's conditional SQL processor, these tests call the
// real order lifecycle transaction. They never construct a Redis client.
type timeoutLifecycleFixture struct {
	db                *gorm.DB
	service           *order.Service
	base              int64
	merchant, voucher string
	users, ids        []string
	owner             map[string]string
	seeded            bool
}

func newTimeoutLifecycleFixture(t *testing.T, db *gorm.DB) *timeoutLifecycleFixture {
	t.Helper()
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("cannot allocate lifecycle scanner fixture")
	}
	base := int64(9_300_000_000_000_000 + binary.LittleEndian.Uint64(entropy[:])%1_000_000_000_000)
	f := &timeoutLifecycleFixture{db: db, base: base, merchant: strconv.FormatInt(base, 10),
		voucher: strconv.FormatInt(base+20, 10), owner: map[string]string{}, service: order.NewService(db, nil, time.Hour)}
	for index := 0; index < 6; index++ {
		f.users = append(f.users, strconv.FormatInt(base+int64(index)+1, 10))
	}
	var collisions int64
	if db.Raw(`SELECT
	 (SELECT COUNT(*) FROM tb_merchant WHERE id=?) +
	 (SELECT COUNT(*) FROM tb_shop WHERE id=?) +
	 (SELECT COUNT(*) FROM tb_voucher WHERE id=?) +
	 (SELECT COUNT(*) FROM tb_seckill_voucher WHERE voucher_id=?) +
	 (SELECT COUNT(*) FROM tb_user WHERE id IN ?) +
	 (SELECT COUNT(*) FROM tb_voucher_order WHERE id BETWEEN ? AND ?) +
	 (SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ORDER' AND CAST(aggregate_id AS UNSIGNED) BETWEEN ? AND ?)`,
		base, base, f.voucher, f.voucher, f.users, base+40, base+46, base+40, base+46).Scan(&collisions).Error != nil || collisions != 0 {
		t.Fatal("cannot establish lifecycle scanner fixture ownership")
	}
	t.Cleanup(func() { f.cleanup(t) })
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("INSERT INTO tb_merchant(id,name) VALUES(?,'owned GO031 scanner merchant')", base).Error; err != nil {
			return err
		}
		if err := tx.Exec("INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(?,?,'owned GO031 scanner shop',1,'owned scanner fixture',120,30)", base, base).Error; err != nil {
			return err
		}
		for index, user := range f.users {
			phone := fmt.Sprintf("137%08d", (base+int64(index))%100_000_000)
			if err := tx.Exec("INSERT INTO tb_user(id,phone,nick_name) VALUES(?,?,'owned GO031 scanner buyer')", user, phone).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES(?,?,'owned GO031 scanner voucher',990,2000,1,1)", f.voucher, base).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO tb_seckill_voucher
		 (voucher_id,merchant_id,stock,total_stock,publish_status,publish_version,begin_time,end_time,redeem_begin_time,redeem_end_time)
		 VALUES(?,?,10,10,'ACTIVE',1,TIMESTAMPADD(HOUR,-1,NOW()),TIMESTAMPADD(HOUR,2,NOW()),TIMESTAMPADD(HOUR,-1,NOW()),TIMESTAMPADD(HOUR,3,NOW()))`, f.voucher, base).Error
	})
	if err != nil {
		t.Fatal("cannot seed exact lifecycle scanner fixture")
	}
	f.seeded = true
	return f
}

func (f *timeoutLifecycleFixture) add(t *testing.T, deadlineSeconds int) string {
	t.Helper()
	index := len(f.ids)
	if index >= len(f.users) {
		t.Fatal("fixture has no remaining owned buyer")
	}
	id, user := strconv.FormatInt(f.base+40+int64(index), 10), f.users[index]
	f.ids, f.owner[id] = append(f.ids, id), user
	creator := order.NewCreator(f.db, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := creator.Create(ctx, id, user, f.voucher)
	if err != nil || result.State != order.Created {
		t.Fatal("real Creator did not commit the owned scanner order")
	}
	f.exec(t, `UPDATE tb_voucher_order SET pay_deadline_at=TIMESTAMPADD(SECOND,?,NOW())
	 WHERE id=? AND user_id=? AND voucher_id=? AND status=1 AND version=0`, deadlineSeconds, id, user, f.voucher)
	return id
}

func (f *timeoutLifecycleFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := f.db.WithContext(ctx).Exec(query, args...)
	if result.Error != nil || result.RowsAffected != 1 {
		t.Fatal("exact owned lifecycle fixture mutation failed")
	}
}

func (f *timeoutLifecycleFixture) count(t *testing.T, query string, want int64, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var value int64
	if f.db.WithContext(ctx).Raw(query, args...).Scan(&value).Error != nil || value != want {
		t.Fatalf("lifecycle scanner invariant wanted %d, observed %d", want, value)
	}
}

func (f *timeoutLifecycleFixture) cancelled(t *testing.T, id string) {
	t.Helper()
	f.count(t, `SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND user_id=? AND voucher_id=?
	 AND status=4 AND version=1 AND cancel_reason='PAY_TIMEOUT' AND cancel_time>=pay_deadline_at AND redeem_token IS NULL`, 1, id, f.owner[id], f.voucher)
	f.count(t, `SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND merchant_id=? AND shop_id=?
	 AND action='ORDER_CANCELLED' AND operator_type='SYSTEM' AND operator_user_id=0
	 AND detail->>'$.orderStatus'='4' AND detail->>'$.version'='1' AND detail->>'$.reason'='PAY_TIMEOUT'
	 AND JSON_CONTAINS_PATH(detail,'one','$.redeemToken')=0`, 1, id, f.merchant, f.base)
	f.count(t, `SELECT COUNT(*) FROM tb_outbox WHERE event_id=? AND aggregate_type='ORDER' AND aggregate_id=?
	 AND aggregate_version=1 AND event_type='ORDER_CHANGED' AND delivery_status='PENDING'
	 AND payload->>'$.orderId'=? AND payload->>'$.userId'=? AND payload->>'$.voucherId'=? AND payload->>'$.shopId'=?
	 AND payload->>'$.targetStatus'='4' AND payload->>'$.stockRestore'='1' AND payload->>'$.releaseEligibility'='true'
	 AND payload->>'$.version'='1' AND JSON_CONTAINS_PATH(payload,'one','$.redeemToken')=0`, 1,
		"ORDER:"+id+":1:CHANGED", id, id, f.owner[id], f.voucher, f.merchant)
}

func (f *timeoutLifecycleFixture) cleanup(t *testing.T) {
	t.Helper()
	if !f.seeded {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(f.ids) != 0 {
			if err := tx.Exec("DELETE FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id IN ?", f.ids).Error; err != nil {
				return err
			}
			if err := tx.Exec("DELETE FROM tb_audit_log WHERE merchant_id=? AND resource_id IN ?", f.merchant, f.ids).Error; err != nil {
				return err
			}
			if err := tx.Exec("DELETE FROM tb_voucher_order WHERE id IN ? AND voucher_id=? AND user_id IN ?", f.ids, f.voucher, f.users).Error; err != nil {
				return err
			}
		}
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{"DELETE FROM tb_seckill_voucher WHERE voucher_id=? AND merchant_id=?", []any{f.voucher, f.merchant}},
			{"DELETE FROM tb_voucher WHERE id=? AND shop_id=?", []any{f.voucher, f.base}},
			{"DELETE FROM tb_shop WHERE id=? AND merchant_id=?", []any{f.base, f.merchant}},
			{"DELETE FROM tb_user WHERE id IN ?", []any{f.users}},
			{"DELETE FROM tb_merchant WHERE id=?", []any{f.merchant}},
		} {
			if err := tx.Exec(statement.query, statement.args...).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Error("exact lifecycle scanner fixture cleanup failed")
		return
	}
	f.count(t, `SELECT (SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id=?) +
	 (SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id IN ?) +
	 (SELECT COUNT(*) FROM tb_audit_log WHERE merchant_id=?) +
	 (SELECT COUNT(*) FROM tb_seckill_voucher WHERE voucher_id=?) +
	 (SELECT COUNT(*) FROM tb_voucher WHERE id=?) +
	 (SELECT COUNT(*) FROM tb_shop WHERE id=?) +
	 (SELECT COUNT(*) FROM tb_user WHERE id IN ?) +
	 (SELECT COUNT(*) FROM tb_merchant WHERE id=?)`, 0, f.voucher, f.ids, f.merchant, f.voucher, f.voucher, f.base, f.users, f.merchant)
	t.Log("cleanup=exact owned catalog/users/orders/audit/outbox remaining=0 redisClient=none")
}

func TestIntegrationTimeoutLifecycle(t *testing.T) {
	db, _ := integrationDatabase(t)
	ctx := context.Background()
	t.Run("real_lifecycle_closes_due_order_with_inventory_system_audit_and_pending_event_without_redis", func(t *testing.T) {
		f := newTimeoutLifecycleFixture(t, db)
		// Keep the fixture deadline distinct from Creator's one-hour snapshot:
		// MySQL reports zero changed rows when an UPDATE writes the same value.
		due, future, paid := f.add(t, -120), f.add(t, 1800), f.add(t, 1800)
		paidView, err := f.service.Pay(ctx, f.owner[paid], paid)
		if err != nil || paidView.RedeemToken == nil {
			t.Fatal("cannot establish the real paid control order")
		}
		f.exec(t, "UPDATE tb_voucher_order SET pay_deadline_at=TIMESTAMPADD(SECOND,-60,NOW()) WHERE id=? AND user_id=? AND voucher_id=?", paid, f.owner[paid], f.voucher)
		worker := NewWorker(db, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
			if _, owned := f.owner[id]; !owned {
				return false, errors.New("refusing unowned lifecycle scanner candidate")
			}
			return f.service.CancelExpired(ctx, id)
		}))
		if count, err := worker.Poll(ctx); count != 1 || err != nil {
			t.Fatal("real scanner did not close exactly its due unpaid order")
		}
		f.cancelled(t, due)
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", 8, f.voucher)
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=1 AND version=0 AND cancel_time IS NULL", 1, future)
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=2 AND version=1 AND redeem_token=? AND cancel_time IS NULL", 1, paid, *paidView.RedeemToken)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE merchant_id=? AND action='ORDER_CANCELLED'", 1, f.merchant)
		if count, err := worker.Poll(ctx); count != 0 || err != nil {
			t.Fatal("already committed cancellation remained a due candidate")
		}
		f.cancelled(t, due)
		t.Logf("actualLifecycle=order.Service.CancelExpired due=%s future=%s paid=%s attempts=1,0 stock=8 version=1 actor=SYSTEM/0 compensation=PENDING redisClient=none", due, future, paid)
	})
	t.Run("two_real_lifecycle_scanners_commit_only_one_transition", func(t *testing.T) {
		f := newTimeoutLifecycleFixture(t, db)
		id := f.add(t, -120)
		arrived, release := make(chan struct{}, 2), make(chan struct{})
		var successes atomic.Int32
		processor := lifecycleFunc(func(ctx context.Context, received string) (bool, error) {
			if received != id {
				return false, errors.New("refusing unowned lifecycle scanner candidate")
			}
			arrived <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return false, ctx.Err()
			}
			changed, err := f.service.CancelExpired(ctx, received)
			if changed {
				successes.Add(1)
			}
			return changed, err
		})
		workers := []*Worker{NewWorker(db, processor), NewWorker(db, processor)}
		counts, failures := make([]int, 2), make([]error, 2)
		var wait sync.WaitGroup
		for index, worker := range workers {
			wait.Add(1)
			go func(index int, worker *Worker) { defer wait.Done(); counts[index], failures[index] = worker.Poll(ctx) }(index, worker)
		}
		for index := 0; index < 2; index++ {
			select {
			case <-arrived:
			case <-time.After(3 * time.Second):
				close(release)
				wait.Wait()
				t.Fatal("two real scanners did not both select the same candidate")
			}
		}
		close(release)
		wait.Wait()
		if counts[0] != 1 || counts[1] != 1 || failures[0] != nil || failures[1] != nil || successes.Load() != 1 {
			t.Fatal("real lifecycle did not serialize overlapping scanner transactions")
		}
		f.cancelled(t, id)
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", 10, f.voucher)
		t.Logf("actualLifecycle=order.Service.CancelExpired order=%s scanners=2 selected=2 committed=1 version=1 stock=10 systemAudits=1 pendingEvents=1", id)
	})
	t.Run("durable_outbox_conflict_rolls_back_first_row_without_starving_second_and_recovers_on_rescan", func(t *testing.T) {
		f := newTimeoutLifecycleFixture(t, db)
		first, second := f.add(t, -120), f.add(t, -60)
		conflictID := "ORDER:" + first + ":1:CHANGED"
		const fault = "owned GO031 scanner semantic conflict"
		payload, _ := json.Marshal(map[string]string{"orderId": first, "userId": f.owner[first], "voucherId": f.voucher, "fault": fault})
		f.exec(t, `INSERT INTO tb_outbox(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload)
	 VALUES(?,'ORDER',?,1,'ORDER_CHANGED',?)`, conflictID, first, string(payload))
		failures := map[string]error{}
		worker := NewWorker(db, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
			if _, owned := f.owner[id]; !owned {
				return false, errors.New("refusing unowned lifecycle scanner candidate")
			}
			changed, err := f.service.CancelExpired(ctx, id)
			failures[id] = err
			return changed, err
		}))
		if count, err := worker.Poll(ctx); count != 2 || err == nil {
			t.Fatal("a failed real lifecycle row did not leave later candidates reachable")
		}
		if !errors.Is(failures[first], outbox.ErrConflict) || failures[second] != nil {
			t.Fatal("controlled failure was not the exact persisted outbox semantic conflict")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=1 AND version=0 AND cancel_time IS NULL AND cancel_reason IS NULL", 1, first)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE merchant_id=? AND resource_id=? AND action='ORDER_CANCELLED'", 0, f.merchant, first)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=? AND payload->>'$.fault'=? AND delivery_status='PENDING'", 1, conflictID, fault)
		f.cancelled(t, second)
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", 9, f.voucher)
		t.Logf("fault=durable semantic outbox conflict first=%s rollback=status1/version0/noCancelAudit second=%s committed=status4/version1 stock=9", first, second)
		f.exec(t, "DELETE FROM tb_outbox WHERE event_id=? AND aggregate_type='ORDER' AND aggregate_id=? AND payload->>'$.fault'=?", conflictID, first, fault)
		if count, err := worker.Poll(ctx); count != 1 || err != nil {
			t.Fatal("removing only the owned durable fault did not recover the DB retry source")
		}
		f.cancelled(t, first)
		f.cancelled(t, second)
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", 10, f.voucher)
		if count, err := worker.Poll(ctx); count != 0 || err != nil {
			t.Fatal("recovered lifecycle transitions were not idempotently complete")
		}
		t.Logf("recovery=removed exact owned conflicting row first=%s rescanAttempts=1,0 committed=1 version=1 stock=10 systemAudits=2 pendingEvents=2", first)
	})
}
