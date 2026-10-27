package order

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func TestIntegrationPay(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	resources, err := database.Open(startup, cfg)
	cancel()
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("dependency cleanup failed")
		}
	})
	lockCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	connection, err := resources.SQL.Conn(lockCtx)
	if err != nil {
		t.Fatal("cannot reserve integration lock connection")
	}
	var acquired int
	if connection.QueryRowContext(lockCtx, "SELECT GET_LOCK('go031_integration',30)").Scan(&acquired) != nil || acquired != 1 {
		_ = connection.Close()
		t.Fatal("another lifecycle integration owns this fixture window")
	}
	t.Cleanup(func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released int
		if connection.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK('go031_integration')").Scan(&released) != nil || released != 1 {
			t.Error("cannot release lifecycle integration lock")
		}
		_ = connection.Close()
	})
	service := NewService(resources.ORM, resources.Redis, 15*time.Minute)
	creator := NewCreator(resources.ORM, 15*time.Minute)
	identity := auth.NewService(resources.ORM, resources.Redis, "development")
	router := httpapi.NewRouter(resources, time.Second, "pay-integration", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) { auth.Register(routes, identity); Register(routes, service, identity) })
	f := newFixture(t, resources, router)
	ctx := context.Background()
	newOrder := func(t *testing.T) (string, string) {
		t.Helper()
		voucher, id := f.activity(t, 10), f.nextID()
		if outcome, err := creator.Create(ctx, id, f.users["A"], voucher); err != nil || outcome.State != Created {
			t.Fatal("cannot create exact owned unpaid fixture")
		}
		return voucher, id
	}
	t.Run("pay_commits_current_snapshot_audit_and_event_without_inventory_change", func(t *testing.T) {
		voucher, id := newOrder(t)
		before, err := service.Get(ctx, f.users["A"], id)
		if err != nil {
			t.Fatal("cannot read original deadline snapshot")
		}
		reply := f.call(t, "A", http.MethodPost, "/voucher-order/"+id+"/pay", map[string]string{"userId": f.users["B"], "redeemToken": "ignored-client-input"})
		var paid View
		if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &paid) != nil || paid.OrderStatus == nil || *paid.OrderStatus != 2 || paid.RedeemToken == nil || paid.PayTime == nil {
			t.Fatal("owned simulated payment did not return its committed paid snapshot")
		}
		decoded, err := hex.DecodeString(*paid.RedeemToken)
		if err != nil || len(decoded) != 32 {
			t.Fatal("payment did not persist a 256-bit hex token")
		}
		if *paid.PayDeadlineAt != *before.PayDeadlineAt || *paid.PayAmount != *before.PayAmount {
			t.Fatal("payment recalculated a historical deadline or amount")
		}
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=2 AND version=1 AND pay_time IS NOT NULL", []any{id}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_PAID' AND operator_user_id=? AND operator_type='USER' AND JSON_CONTAINS_PATH(detail,'one','$.redeemToken')=0", []any{id, f.users["A"]}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=? AND event_type='ORDER_CHANGED' AND aggregate_version=1 AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.targetStatus'))='2' AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.stockRestore'))='0' AND JSON_CONTAINS_PATH(payload,'one','$.redeemToken')=0", []any{"ORDER:" + id + ":1:CHANGED"}, 1)
	})
	t.Run("twenty_concurrent_payments_are_one_transition_and_return_the_same_token", func(t *testing.T) {
		voucher, id := newOrder(t)
		const requests = 20
		start := make(chan struct{})
		var wait sync.WaitGroup
		views := make([]View, requests)
		errs := make([]error, requests)
		for i := range requests {
			wait.Add(1)
			go func(i int) { defer wait.Done(); <-start; views[i], errs[i] = service.Pay(ctx, f.users["A"], id) }(i)
		}
		close(start)
		wait.Wait()
		var token string
		for i := range requests {
			if errs[i] != nil || views[i].OrderStatus == nil || *views[i].OrderStatus != 2 || views[i].RedeemToken == nil {
				t.Fatal("concurrent same-action payment did not return the paid order")
			}
			if i == 0 {
				token = *views[i].RedeemToken
			}
			if *views[i].RedeemToken != token {
				t.Fatal("idempotent payment replaced the original token")
			}
		}
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
		f.count(t, "SELECT version FROM tb_voucher_order WHERE id=?", []any{id}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_PAID'", []any{id}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=?", []any{"ORDER:" + id + ":1:CHANGED"}, 1)
	})
	t.Run("foreign_or_missing_orders_are_404_and_terminal_conflicts_have_no_token", func(t *testing.T) {
		_, id := newOrder(t)
		for _, path := range []string{"/voucher-order/" + id + "/pay", "/voucher-order/" + f.nextID() + "/pay"} {
			reply := f.call(t, "B", http.MethodPost, path, nil)
			if reply.Code != 404 || reply.Success || len(reply.Data) != 0 {
				t.Fatal("foreign and missing orders must be equally invisible")
			}
		}
		for _, status := range []int{3, 4, 6} {
			f.exec(t, "UPDATE tb_voucher_order SET status=? WHERE id=?", status, id)
			reply := f.call(t, "A", http.MethodPost, "/voucher-order/"+id+"/pay", nil)
			if reply.Code != 409 || reply.Success || len(reply.Data) != 0 {
				t.Fatal("another terminal action cannot become a successful payment")
			}
		}
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_PAID'", []any{id}, 0)
	})
	t.Run("db_deadline_boundary_and_old_configuration_never_allow_late_payment", func(t *testing.T) {
		_, id := newOrder(t)
		f.exec(t, "UPDATE tb_voucher_order SET pay_deadline_at=NOW() WHERE id=?", id)
		configuredLater := NewService(resources.ORM, resources.Redis, 24*time.Hour)
		if _, err := configuredLater.Pay(ctx, f.users["A"], id); !errors.Is(err, ErrConflict) {
			t.Fatal("deadline equality or a new timeout configuration allowed a late payment")
		}
		f.exec(t, "UPDATE tb_voucher_order SET pay_deadline_at=DATE_SUB(NOW(),INTERVAL 1 SECOND) WHERE id=?", id)
		if _, err := service.Pay(ctx, f.users["A"], id); !errors.Is(err, ErrConflict) {
			t.Fatal("expired unpaid order was paid")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=1 AND version=0 AND pay_time IS NULL AND redeem_token IS NULL", []any{id}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=?", []any{"ORDER:" + id + ":1:CHANGED"}, 0)
	})
	t.Run("audit_outbox_and_entropy_failures_roll_back_the_complete_transition", func(t *testing.T) {
		callback := "go031_pay_persistence_failure"
		if err := resources.ORM.Callback().Raw().Before("gorm:raw").Register(callback, func(tx *gorm.DB) {
			fault, _ := tx.Statement.Context.Value(faultKey{}).(string)
			query := tx.Statement.SQL.String()
			if (fault == "pay-audit" && strings.Contains(query, "INSERT INTO tb_audit_log")) || (fault == "pay-outbox" && strings.Contains(query, "INSERT INTO tb_outbox")) {
				tx.AddError(errors.New("injected owned payment persistence failure"))
			}
		}); err != nil {
			t.Fatal("cannot inject payment persistence fault")
		}
		t.Cleanup(func() { _ = resources.ORM.Callback().Raw().Remove(callback) })
		for _, fault := range []string{"pay-audit", "pay-outbox", "pay-entropy"} {
			voucher, id := newOrder(t)
			payService := NewService(resources.ORM, resources.Redis, 15*time.Minute)
			if fault == "pay-entropy" {
				payService.newRedeemToken = func() (string, error) { return "", errors.New("injected entropy failure") }
			}
			view, err := payService.Pay(context.WithValue(ctx, faultKey{}, fault), f.users["A"], id)
			if err == nil || view.ID != "" || view.RedeemToken != nil {
				t.Fatal("failed payment fabricated a committed result")
			}
			f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=1 AND version=0 AND pay_time IS NULL AND redeem_token IS NULL", []any{id}, 1)
			f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
			f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_PAID'", []any{id}, 0)
			f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=?", []any{"ORDER:" + id + ":1:CHANGED"}, 0)
		}
	})
	t.Run("tokens_are_unique_and_collision_retries_are_bounded_and_rollback_first", func(t *testing.T) {
		_, first := newOrder(t)
		paid, err := service.Pay(ctx, f.users["A"], first)
		if err != nil || paid.RedeemToken == nil {
			t.Fatal("cannot prepare token uniqueness fixture")
		}
		_, second := newOrder(t)
		secondService := NewService(resources.ORM, resources.Redis, 15*time.Minute)
		var generated atomic.Int32
		secondService.newRedeemToken = func() (string, error) {
			if generated.Add(1) == 1 {
				return *paid.RedeemToken, nil
			}
			return secureRedeemToken()
		}
		other, err := secondService.Pay(ctx, f.users["A"], second)
		if err != nil || other.RedeemToken == nil || *other.RedeemToken == *paid.RedeemToken || generated.Load() != 2 {
			t.Fatal("unique token collision did not retry in a fresh transaction")
		}
		_, third := newOrder(t)
		alwaysCollision := NewService(resources.ORM, resources.Redis, 15*time.Minute)
		var attempts atomic.Int32
		alwaysCollision.newRedeemToken = func() (string, error) { attempts.Add(1); return *paid.RedeemToken, nil }
		if view, err := alwaysCollision.Pay(ctx, f.users["A"], third); err == nil || view.ID != "" || attempts.Load() != 3 {
			t.Fatal("repeated token collision was not bounded or returned a fake success")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=1 AND version=0 AND redeem_token IS NULL", []any{third}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_PAID'", []any{third}, 0)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_PAID'", []any{second}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=?", []any{"ORDER:" + second + ":1:CHANGED"}, 1)
	})
	t.Run("version_bounds_reject_before_writes_and_tokens_stay_out_of_lists_and_terminals", func(t *testing.T) {
		_, id := newOrder(t)
		for _, version := range []int{-1, math.MaxInt32} {
			f.exec(t, "UPDATE tb_voucher_order SET version=? WHERE id=?", version, id)
			if _, err := service.Pay(ctx, f.users["A"], id); !errors.Is(err, ErrUnsafe) {
				t.Fatal("version bounds must stop an unsafe increment")
			}
		}
		f.exec(t, "UPDATE tb_voucher_order SET version=0 WHERE id=?", id)
		if _, err := service.Pay(ctx, f.users["A"], id); err != nil {
			t.Fatal("cannot pay privacy fixture")
		}
		items, _, err := service.List(ctx, f.users["A"], Query{Current: 1, PageSize: 50})
		if err != nil {
			t.Fatal("cannot list privacy fixture")
		}
		for _, item := range items {
			if item.RedeemToken != nil {
				t.Fatal("a list disclosed a redeem token")
			}
		}
		for _, status := range []int{3, 4, 6} {
			f.exec(t, "UPDATE tb_voucher_order SET status=? WHERE id=?", status, id)
			view, err := service.Get(ctx, f.users["A"], id)
			if err != nil || view.RedeemToken != nil {
				t.Fatal("terminal order detail disclosed a redeem token")
			}
		}
	})
	t.Run("payment_waiting_for_inventory_lock_rechecks_db_time_after_deadline", func(t *testing.T) {
		voucher, id := newOrder(t)
		f.exec(t, "UPDATE tb_voucher_order SET pay_deadline_at=DATE_ADD(NOW(),INTERVAL 2 SECOND) WHERE id=?", id)
		blocker, err := resources.SQL.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("cannot hold current inventory row")
		}
		defer blocker.Rollback()
		var stock int
		if blocker.QueryRowContext(ctx, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=? FOR UPDATE", voucher).Scan(&stock) != nil {
			t.Fatal("cannot lock exact inventory fixture")
		}
		arrived, resumed := make(chan struct{}, 1), make(chan struct{})
		callback := "go031_observe_pay_inventory_wait"
		if err := resources.ORM.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Context.Value(faultKey{}) == "pay-wait" && strings.Contains(tx.Statement.SQL.String(), "FROM tb_seckill_voucher WHERE voucher_id=? FOR UPDATE") {
				select {
				case arrived <- struct{}{}:
				default:
				}
			}
		}); err != nil {
			t.Fatal("cannot observe current inventory read")
		}
		t.Cleanup(func() { _ = resources.ORM.Callback().Row().Remove(callback) })
		result := make(chan error, 1)
		payCtx, cancel := context.WithTimeout(context.WithValue(ctx, faultKey{}, "pay-wait"), 8*time.Second)
		defer cancel()
		go func() { defer close(resumed); _, err := service.Pay(payCtx, f.users["A"], id); result <- err }()
		t.Cleanup(func() {
			cancel()
			select {
			case <-resumed:
			case <-time.After(5 * time.Second):
				t.Error("waiting Pay did not finish before fixture cleanup")
			}
		})
		select {
		case <-arrived:
		case <-time.After(3 * time.Second):
			t.Fatal("Pay did not reach its current inventory locking query")
		}
		deadlineCtx, cancelClock := context.WithTimeout(ctx, 4*time.Second)
		defer cancelClock()
		for {
			var expired bool
			if err := resources.SQL.QueryRowContext(deadlineCtx, "SELECT NOW(3)>=pay_deadline_at FROM tb_voucher_order WHERE id=?", id).Scan(&expired); err != nil {
				t.Fatal("cannot observe DB deadline")
			}
			if expired {
				break
			}
			select {
			case <-result:
				t.Fatal("Pay bypassed the held inventory lock")
			case <-time.After(20 * time.Millisecond):
			case <-deadlineCtx.Done():
				t.Fatal("DB deadline did not arrive in bounded window")
			}
		}
		if err := blocker.Commit(); err != nil {
			t.Fatal("cannot release inventory fixture")
		}
		select {
		case err := <-result:
			if !errors.Is(err, ErrConflict) {
				t.Fatal("lock-waiting payment used stale time and crossed the deadline")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Pay did not finish after current lock release")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=1 AND version=0 AND pay_time IS NULL AND redeem_token IS NULL", []any{id}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=?", []any{"ORDER:" + id + ":1:CHANGED"}, 0)
	})
}
