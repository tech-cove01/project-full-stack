package merchantorder

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/activity"
	"github.com/cityloop/fullstack-go/internal/activitypub"
	"github.com/cityloop/fullstack-go/internal/order"
	"github.com/cityloop/fullstack-go/internal/ordercomp"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// Each subtest owns a separate activity and exact random identifiers. No API,
// stream worker, global flush, sequence reset, or database DDL is performed.
func TestIntegrationBatchTrade(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("requires isolated MySQL/Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated configuration")
	}
	r, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })

	for _, action := range []string{"redeem", "refund"} {
		t.Run(action+"_cannot_use_a_revoked_membership_after_precheck", func(t *testing.T) {
			f := newBatchFixture(t, r, 2)
			buyer := order.NewService(r.ORM, r.Redis, 15*time.Minute)
			if _, err := order.NewCreator(r.ORM, 15*time.Minute).Create(context.Background(), f.order, f.buyer, f.voucher); err != nil {
				t.Fatal(err)
			}
			paid, err := buyer.Pay(context.Background(), f.buyer, f.order)
			if err != nil || paid.RedeemToken == nil {
				t.Fatal("cannot prepare paid order")
			}
			name := "review-revoke-" + f.order
			var once sync.Once
			err = r.ORM.Callback().Row().After("gorm:row").Register(name, func(tx *gorm.DB) {
				if strings.Contains(tx.Statement.SQL.String(), "SELECT mm.id, mm.merchant_id, mm.role_code") {
					once.Do(func() {
						if e := r.ORM.Exec("UPDATE tb_merchant_member SET status='DISABLED' WHERE id=?", f.member).Error; e != nil {
							t.Error(e)
						}
					})
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.ORM.Callback().Row().Remove(name) })
			svc := NewService(r.ORM)
			if action == "redeem" {
				_, err = svc.Redeem(context.Background(), f.operator, f.shop, *paid.RedeemToken)
			} else {
				_, err = svc.Refund(context.Background(), f.operator, f.order, "review")
			}
			if err == nil {
				t.Fatal("revoked membership performed a merchant write")
			}
			f.count(t, "SELECT status FROM tb_voucher_order WHERE id=?", f.order, 2)
			f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action IN ('ORDER_REDEEMED','ORDER_REFUNDED')", f.order, 0)
		})
	}
	t.Run("redeem_refund_race_commits_one_terminal_effect", func(t *testing.T) {
		f := newBatchFixture(t, r, 2)
		ctx := context.Background()
		if _, err := order.NewCreator(r.ORM, 15*time.Minute).Create(ctx, f.order, f.buyer, f.voucher); err != nil {
			t.Fatal(err)
		}
		paid, err := order.NewService(r.ORM, r.Redis, 15*time.Minute).Pay(ctx, f.buyer, f.order)
		if err != nil || paid.RedeemToken == nil {
			t.Fatal("paid fixture failed")
		}
		svc := NewService(r.ORM)
		start := make(chan struct{})
		var wait sync.WaitGroup
		for i := 0; i < 20; i++ {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				<-start
				if i%2 == 0 {
					_, _ = svc.Redeem(ctx, f.operator, f.shop, *paid.RedeemToken)
				} else {
					_, _ = svc.Refund(ctx, f.operator, f.order, "review-race")
				}
			}(i)
		}
		close(start)
		wait.Wait()
		var status int
		if err := r.ORM.Raw("SELECT status FROM tb_voucher_order WHERE id=?", f.order).Scan(&status).Error; err != nil || (status != 3 && status != 6) {
			t.Fatal("race has no valid terminal result")
		}
		wantStock := 1
		if status == 6 {
			wantStock = 2
		}
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", f.voucher, wantStock)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action IN ('ORDER_REDEEMED','ORDER_REFUNDED')", f.order, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=?", f.order, 3)
	})
	t.Run("durable_rejection_is_claimed_and_compensated_once", func(t *testing.T) {
		f := newBatchFixture(t, r, 0)
		f.exec(t, "UPDATE tb_seckill_voucher SET total_stock=1 WHERE voucher_id=?", f.voucher)
		ctx := context.Background()
		for _, arg := range []struct{ key, value string }{{"seckill:stock:" + f.voucher, "0"}} {
			if r.Redis.Set(ctx, arg.key, arg.value, 0).Err() != nil {
				t.Fatal("redis seed failed")
			}
		}
		if r.Redis.SAdd(ctx, "seckill:order:"+f.voucher, f.buyer).Err() != nil || r.Redis.HSet(ctx, "seckill:accepted:"+f.voucher, f.buyer, f.order).Err() != nil {
			t.Fatal("redis ownership seed failed")
		}
		outcome, err := order.NewCreator(r.ORM, 15*time.Minute).Create(ctx, f.order, f.buyer, f.voucher)
		if err != nil || outcome.State != order.Failed {
			t.Fatal("cannot prepare durable rejection")
		}
		processed, err := ordercomp.NewWorker(r.ORM, r.Redis).Poll(ctx)
		if err != nil || !processed {
			t.Fatalf("durable rejection was never claimed: processed=%v err=%v", processed, err)
		}
		if stock, e := r.Redis.Get(ctx, "seckill:stock:"+f.voucher).Result(); e != nil || stock != "1" {
			t.Fatal("rejected reservation stock not restored")
		}
		if r.Redis.SIsMember(ctx, "seckill:order:"+f.voucher, f.buyer).Val() || r.Redis.HExists(ctx, "seckill:accepted:"+f.voucher, f.buyer).Val() {
			t.Fatal("failed reservation ownership not released")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=? AND delivery_status='DONE'", f.order, 1)
		if again, e := ordercomp.NewWorker(r.ORM, r.Redis).Poll(ctx); e != nil || again {
			t.Fatal("done rejection was claimed twice")
		}
	})
	t.Run("rebuild_blocks_unapplied_rejection", func(t *testing.T) {
		f := newBatchFixture(t, r, 1)
		ctx := context.Background()
		f.exec(t, "INSERT INTO tb_outbox(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload) VALUES(?, 'RESERVATION',?,0,'RESERVATION_REJECTED',JSON_OBJECT('orderId',?,'userId',?,'voucherId',?,'stockDelta',1,'releasePurchase',true,'reason','review'))", "RESERVED:"+f.order+":REJECTED", f.order, f.order, f.buyer, f.voucher)
		// Give XINFO a valid idle group: no message has been accepted here.
		if err := r.Redis.XGroupCreateMkStream(ctx, "stream.orders", "g1", "0").Err(); err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
			t.Fatal(err)
		}
		_, err := activity.NewService(r.ORM, r.Redis).Reconcile(ctx, f.operator, f.voucher, true)
		if err == nil {
			t.Fatal("rebuild ignored a durable rejection awaiting compensation")
		}
		if !strings.Contains(err.Error(), "OUTBOX_PENDING") {
			t.Fatalf("expected pending rejection blocker, got %v", err)
		}
		f.count(t, "SELECT publish_version FROM tb_seckill_voucher WHERE voucher_id=?", f.voucher, 1)
	})
	t.Run("rebuild_can_retry_after_redis_rewrite_and_db_audit_rollback", func(t *testing.T) {
		f := newBatchFixture(t, r, 1)
		name := "review-rebuild-rollback-" + f.order
		if err := r.ORM.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
			if tx.Statement.Table == "tb_audit_log" {
				tx.AddError(errors.New("review audit rollback"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		svc := activity.NewService(r.ORM, r.Redis)
		_, first := svc.Reconcile(context.Background(), f.operator, f.voucher, true)
		_ = r.ORM.Callback().Create().Remove(name)
		if first == nil {
			t.Fatal("fault did not roll back reconciliation")
		}
		f.count(t, "SELECT publish_version FROM tb_seckill_voucher WHERE voucher_id=?", f.voucher, 1)
		if _, err := svc.Reconcile(context.Background(), f.operator, f.voucher, true); err != nil {
			t.Fatalf("partial Redis rewrite made controlled retry impossible: %v", err)
		}
	})
}

type batchFixture struct {
	r                                                       *database.Resources
	merchant, shop, member, operator, buyer, voucher, order string
}

func newBatchFixture(t *testing.T, r *database.Resources, stock int) *batchFixture {
	t.Helper()
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	base := uint64(9400000000000000) + (binary.BigEndian.Uint64(entropy[:])%1000000000)*16
	id := func(n uint64) string { return fmt.Sprint(base + n) }
	f := &batchFixture{r: r, merchant: id(0), shop: id(0), member: id(0), operator: id(0), buyer: id(1), voucher: id(0), order: id(0)}
	t.Cleanup(func() {
		for _, q := range []string{"DELETE FROM tb_outbox WHERE aggregate_id=?", "DELETE FROM tb_audit_log WHERE resource_id=?", "DELETE FROM tb_voucher_order WHERE id=?"} {
			if e := r.ORM.Exec(q, f.order).Error; e != nil {
				t.Error("order cleanup failed")
			}
		}
		for _, q := range []string{"DELETE FROM tb_outbox WHERE aggregate_id=?", "DELETE FROM tb_seckill_voucher WHERE voucher_id=?", "DELETE FROM tb_voucher WHERE id=?"} {
			if e := r.ORM.Exec(q, f.voucher).Error; e != nil {
				t.Error("activity cleanup failed")
			}
		}
		for _, q := range []string{"DELETE FROM tb_member_shop_scope WHERE member_id=?", "DELETE FROM tb_merchant_member WHERE id=?", "DELETE FROM tb_shop WHERE id=?", "DELETE FROM tb_merchant WHERE id=?"} {
			if e := r.ORM.Exec(q, f.merchant).Error; e != nil {
				t.Error("merchant cleanup failed")
			}
		}
		_ = r.ORM.Exec("DELETE FROM tb_user WHERE id IN (?,?)", f.operator, f.buyer).Error
		keys := []string{"comp:done:RESERVED:" + f.order + ":REJECTED", "order:reservation:" + f.order}
		for _, kind := range []string{"gate", "stock", "begin", "end", "order", "accepted"} {
			keys = append(keys, "seckill:"+kind+":"+f.voucher)
		}
		if e := r.Redis.Del(context.Background(), keys...).Err(); e != nil {
			t.Error("redis cleanup failed")
		}
	})
	f.exec(t, "INSERT INTO tb_user(id,phone,nick_name) VALUES(?,?,'review-operator'),(?,?,'review-buyer')", f.operator, fmt.Sprintf("138%08d", base%100000000), f.buyer, fmt.Sprintf("138%08d", (base+1)%100000000))
	f.exec(t, "INSERT INTO tb_merchant(id,name) VALUES(?,'trade-review')", f.merchant)
	f.exec(t, "INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(?,?,'trade-review',1,'review',120,30)", f.shop, f.merchant)
	f.exec(t, "INSERT INTO tb_merchant_member(id,merchant_id,user_id,role_code) VALUES(?,?,?,'OWNER')", f.member, f.merchant, f.operator)
	f.exec(t, "INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES(?,?,'trade-review',1000,2000,1,1)", f.voucher, f.shop)
	f.exec(t, "INSERT INTO tb_seckill_voucher(voucher_id,merchant_id,stock,total_stock,publish_status,publish_version,begin_time,end_time,redeem_begin_time,redeem_end_time) VALUES(?,?,?,?,'ACTIVE',1,DATE_SUB(NOW(),INTERVAL 1 HOUR),DATE_ADD(NOW(),INTERVAL 1 DAY),DATE_SUB(NOW(),INTERVAL 1 HOUR),DATE_ADD(NOW(),INTERVAL 2 DAY))", f.voucher, f.merchant, stock, stock)
	gate := activitypub.NewGate(r.Redis)
	now := time.Now()
	if e := gate.Prepare(context.Background(), f.voucher, 1, stock, now.Add(-time.Hour).Truncate(time.Second), now.Add(24*time.Hour).Truncate(time.Second)); e != nil {
		t.Fatal(e)
	}
	if e := gate.Open(context.Background(), f.voucher, 1); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *batchFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if err := f.r.ORM.Exec(q, args...).Error; err != nil {
		t.Fatal(err)
	}
}
func (f *batchFixture) count(t *testing.T, q string, arg string, want int) {
	t.Helper()
	var got int
	if e := f.r.ORM.Raw(q, arg).Scan(&got).Error; e != nil || got != want {
		t.Fatalf("durable state count=%d want=%d err=%v", got, want, e)
	}
}
