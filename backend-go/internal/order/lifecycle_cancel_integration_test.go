package order

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func TestIntegrationCancel(t *testing.T) {
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
		t.Fatal("cannot reserve lifecycle integration lock")
	}
	var acquired int
	if connection.QueryRowContext(lockCtx, "SELECT GET_LOCK('go031_integration',30)").Scan(&acquired) != nil || acquired != 1 {
		_ = connection.Close()
		t.Fatal("another lifecycle suite owns this fixture window")
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
	router := httpapi.NewRouter(resources, time.Second, "cancel-integration", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) { auth.Register(routes, identity); Register(routes, service, identity) })
	f := newFixture(t, resources, router)
	ctx := context.Background()
	newOrder := func(t *testing.T) (string, string) {
		t.Helper()
		voucher, id := f.activity(t, 10), f.nextID()
		if outcome, err := creator.Create(ctx, id, f.users["A"], voucher); err != nil || outcome.State != Created {
			t.Fatal("cannot create owned cancellation fixture")
		}
		return voucher, id
	}
	assertCancelled := func(t *testing.T, voucher, id, reason, operator, operatorType string) {
		t.Helper()
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 10)
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=4 AND version=1 AND cancel_time IS NOT NULL AND cancel_reason=? AND redeem_token IS NULL", []any{id, reason}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CANCELLED' AND operator_user_id=? AND operator_type=?", []any{id, operator, operatorType}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=? AND delivery_status='PENDING' AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.targetStatus'))='4' AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.stockRestore'))='1' AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.releaseEligibility'))='true' AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.userId'))=?", []any{"ORDER:" + id + ":1:CHANGED", f.users["A"]}, 1)
	}
	t.Run("owned_cancel_clears_token_and_commits_one_restore_audit_and_event", func(t *testing.T) {
		voucher, id := newOrder(t)
		f.exec(t, "UPDATE tb_voucher_order SET redeem_token=? WHERE id=?", "owned-dirty-unpaid-token-"+id, id)
		reply := f.call(t, "A", http.MethodPost, "/voucher-order/"+id+"/cancel", map[string]string{"userId": f.users["B"]})
		var cancelled View
		if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &cancelled) != nil || cancelled.OrderStatus == nil || *cancelled.OrderStatus != 4 || cancelled.CancelTime == nil || cancelled.CancelReason == nil || *cancelled.CancelReason != "USER_CANCEL" || cancelled.RedeemToken != nil {
			t.Fatal("owned cancellation did not return its committed cancellation snapshot")
		}
		assertCancelled(t, voucher, id, "USER_CANCEL", f.users["A"], "USER")
		repeated, err := service.Cancel(ctx, f.users["A"], id, "another reason")
		if err != nil || repeated.CancelTime == nil || *repeated.CancelTime != *cancelled.CancelTime || *repeated.CancelReason != "USER_CANCEL" {
			t.Fatal("idempotent cancellation changed its original reason or timestamp")
		}
		assertCancelled(t, voucher, id, "USER_CANCEL", f.users["A"], "USER")
	})
	t.Run("twenty_concurrent_cancels_restore_inventory_only_once", func(t *testing.T) {
		voucher, id := newOrder(t)
		const requests = 20
		start := make(chan struct{})
		var wait sync.WaitGroup
		views := make([]View, requests)
		errs := make([]error, requests)
		for i := range requests {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				<-start
				views[i], errs[i] = service.Cancel(ctx, f.users["A"], id, "CONCURRENT_CANCEL")
			}(i)
		}
		close(start)
		wait.Wait()
		for i := range requests {
			if errs[i] != nil || views[i].OrderStatus == nil || *views[i].OrderStatus != 4 || views[i].RedeemToken != nil {
				t.Fatal("concurrent same-action cancellation did not return the one cancelled order")
			}
		}
		assertCancelled(t, voucher, id, "CONCURRENT_CANCEL", f.users["A"], "USER")
	})
	t.Run("foreign_missing_paid_and_other_terminal_orders_do_not_change", func(t *testing.T) {
		voucher, id := newOrder(t)
		for _, path := range []string{"/voucher-order/" + id + "/cancel", "/voucher-order/" + f.nextID() + "/cancel"} {
			reply := f.call(t, "B", http.MethodPost, path, nil)
			if reply.Code != 404 || reply.Success || len(reply.Data) != 0 {
				t.Fatal("foreign and missing cancellation targets must be equally invisible")
			}
		}
		if _, err := service.Pay(ctx, f.users["A"], id); err != nil {
			t.Fatal("cannot pay cancellation conflict fixture")
		}
		for _, status := range []int{2, 3, 6} {
			f.exec(t, "UPDATE tb_voucher_order SET status=? WHERE id=?", status, id)
			reply := f.call(t, "A", http.MethodPost, "/voucher-order/"+id+"/cancel", nil)
			if reply.Code != 409 || reply.Success || len(reply.Data) != 0 {
				t.Fatal("paid/redeemed/refunded cancellation must conflict without exposing a token")
			}
		}
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CANCELLED'", []any{id}, 0)
	})
	t.Run("pay_and_cancel_compete_for_one_transition_in_twelve_rounds", func(t *testing.T) {
		for round := 0; round < 12; round++ {
			voucher, id := newOrder(t)
			start := make(chan struct{})
			var wait sync.WaitGroup
			var payErr, cancelErr error
			wait.Add(2)
			go func() { defer wait.Done(); <-start; _, payErr = service.Pay(ctx, f.users["A"], id) }()
			go func() {
				defer wait.Done()
				<-start
				_, cancelErr = service.Cancel(ctx, f.users["A"], id, "RACE_CANCEL")
			}()
			close(start)
			wait.Wait()
			if (payErr == nil) == (cancelErr == nil) {
				t.Fatal("payment and cancellation did not choose exactly one first transition")
			}
			if payErr != nil && !errors.Is(payErr, ErrConflict) {
				t.Fatal("losing payment did not return a state conflict")
			}
			if cancelErr != nil && !errors.Is(cancelErr, ErrConflict) {
				t.Fatal("losing cancellation did not return a state conflict")
			}
			var status int
			if resources.SQL.QueryRow("SELECT status FROM tb_voucher_order WHERE id=?", id).Scan(&status) != nil {
				t.Fatal("cannot inspect race outcome")
			}
			stock := 9
			if status == 4 {
				stock = 10
			} else if status != 2 {
				t.Fatal("race ended outside paid/cancelled states")
			}
			f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, stock)
			f.count(t, "SELECT version FROM tb_voucher_order WHERE id=?", []any{id}, 1)
			f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action IN ('ORDER_PAID','ORDER_CANCELLED')", []any{id}, 1)
			f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id=? AND aggregate_version=1", []any{id}, 1)
		}
	})
	t.Run("pay_and_expired_scan_choose_valid_outcomes_on_both_sides_of_deadline", func(t *testing.T) {
		for _, due := range []bool{false, true} {
			for round := 0; round < 4; round++ {
				voucher, id := newOrder(t)
				query := "UPDATE tb_voucher_order SET pay_deadline_at=DATE_ADD(NOW(),INTERVAL 1 MINUTE) WHERE id=?"
				if due {
					query = "UPDATE tb_voucher_order SET pay_deadline_at=NOW() WHERE id=?"
				}
				f.exec(t, query, id)
				start := make(chan struct{})
				var wait sync.WaitGroup
				var payErr, expiryErr error
				var changed bool
				wait.Add(2)
				go func() { defer wait.Done(); <-start; _, payErr = service.Pay(ctx, f.users["A"], id) }()
				go func() { defer wait.Done(); <-start; changed, expiryErr = service.CancelExpired(ctx, id) }()
				close(start)
				wait.Wait()
				if expiryErr != nil {
					t.Fatal("expiry competitor returned an unexpected failure")
				}
				if due {
					if !changed || !errors.Is(payErr, ErrConflict) {
						t.Fatal("due order accepted payment or did not expire")
					}
					assertCancelled(t, voucher, id, "PAY_TIMEOUT", "0", "SYSTEM")
				} else {
					if changed || payErr != nil {
						t.Fatal("not-due order was expired instead of paid")
					}
					f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
				}
				f.count(t, "SELECT version FROM tb_voucher_order WHERE id=?", []any{id}, 1)
				f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action IN ('ORDER_PAID','ORDER_CANCELLED')", []any{id}, 1)
			}
		}
	})
	t.Run("scanner_contract_missing_future_terminal_noop_and_due_is_idempotent", func(t *testing.T) {
		if changed, err := service.CancelExpired(ctx, f.nextID()); err != nil || changed {
			t.Fatal("missing scanner candidate must be a no-op")
		}
		voucher, id := newOrder(t)
		if changed, err := service.CancelExpired(ctx, id); err != nil || changed {
			t.Fatal("future scanner candidate must be a no-op")
		}
		for _, status := range []int{2, 3, 4, 6} {
			f.exec(t, "UPDATE tb_voucher_order SET status=?,pay_deadline_at=DATE_SUB(NOW(),INTERVAL 1 SECOND) WHERE id=?", status, id)
			if changed, err := service.CancelExpired(ctx, id); err != nil || changed {
				t.Fatal("terminal scanner candidate must be a no-op")
			}
		}
		f.exec(t, "UPDATE tb_voucher_order SET status=1,pay_deadline_at=NOW() WHERE id=?", id)
		if changed, err := service.CancelExpired(ctx, id); err != nil || !changed {
			t.Fatal("DB deadline equality must permit expiration")
		}
		if changed, err := service.CancelExpired(ctx, id); err != nil || changed {
			t.Fatal("same expired scan must not restore inventory twice")
		}
		assertCancelled(t, voucher, id, "PAY_TIMEOUT", "0", "SYSTEM")
	})
	t.Run("audit_event_and_inventory_failure_roll_back_user_and_system_cancellations", func(t *testing.T) {
		callback := "go031_cancel_persistence_failure"
		if err := resources.ORM.Callback().Raw().Before("gorm:raw").Register(callback, func(tx *gorm.DB) {
			fault, _ := tx.Statement.Context.Value(faultKey{}).(string)
			query := tx.Statement.SQL.String()
			if (fault == "cancel-audit" && strings.Contains(query, "INSERT INTO tb_audit_log")) || (fault == "cancel-event" && strings.Contains(query, "INSERT INTO tb_outbox")) {
				tx.AddError(errors.New("injected owned cancellation persistence failure"))
			}
		}); err != nil {
			t.Fatal("cannot inject exact cancellation fault")
		}
		t.Cleanup(func() { _ = resources.ORM.Callback().Raw().Remove(callback) })
		for _, system := range []bool{false, true} {
			for _, fault := range []string{"cancel-audit", "cancel-event", "inventory-full"} {
				voucher, id := newOrder(t)
				if system {
					f.exec(t, "UPDATE tb_voucher_order SET pay_deadline_at=NOW() WHERE id=?", id)
				}
				stock := 9
				if fault == "inventory-full" {
					f.exec(t, "UPDATE tb_seckill_voucher SET stock=total_stock WHERE voucher_id=?", voucher)
					stock = 10
				}
				faultCtx := context.WithValue(ctx, faultKey{}, fault)
				var failure error
				if system {
					var changed bool
					changed, failure = service.CancelExpired(faultCtx, id)
					if changed {
						t.Fatal("failed expiry reported a committed transition")
					}
				} else {
					var view View
					view, failure = service.Cancel(faultCtx, f.users["A"], id, "ROLLBACK_CANCEL")
					if view.ID != "" {
						t.Fatal("failed user cancellation returned a committed view")
					}
				}
				if failure == nil {
					t.Fatal("invalid inventory/persistence failure was reported as successful cancellation")
				}
				f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=1 AND version=0 AND cancel_time IS NULL AND cancel_reason IS NULL AND redeem_token IS NULL", []any{id}, 1)
				f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, stock)
				f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CANCELLED'", []any{id}, 0)
				f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=?", []any{"ORDER:" + id + ":1:CHANGED"}, 0)
			}
		}
	})
	t.Run("reason_normalization_is_unicode_safe_and_does_not_overwrite_first_cancellation", func(t *testing.T) {
		_, id := newOrder(t)
		input := "  " + strings.Repeat("猫🐱中", 30) + "  "
		reply := f.call(t, "A", http.MethodPost, "/voucher-order/"+id+"/cancel?reason="+url.QueryEscape(input), nil)
		var view View
		if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &view) != nil || view.CancelReason == nil || !utf8.ValidString(*view.CancelReason) || utf8.RuneCountInString(*view.CancelReason) != 64 || *view.CancelReason != string([]rune(strings.Repeat("猫🐱中", 30))[:64]) {
			t.Fatal("API cancellation reason was not trimmed/truncated by Unicode characters")
		}
		original := *view.CancelReason
		if repeated, err := service.Cancel(ctx, f.users["A"], id, "replacement"); err != nil || repeated.CancelReason == nil || *repeated.CancelReason != original {
			t.Fatal("repeated cancellation replaced the committed reason")
		}
		_, invalid := newOrder(t)
		bad := f.call(t, "A", http.MethodPost, "/voucher-order/"+invalid+"/cancel?reason=%FF", nil)
		if bad.Code != 400 || bad.Success {
			t.Fatal("invalid UTF8 reason must fail before mutation")
		}
		f.count(t, "SELECT status FROM tb_voucher_order WHERE id=?", []any{invalid}, 1)
	})
	t.Run("cancelled_creator_replay_preserves_terminal_and_only_db_eligibility_is_released", func(t *testing.T) {
		voucher, id := newOrder(t)
		if resources.Redis.Set(ctx, "seckill:stock:"+voucher, 9, 0).Err() != nil || resources.Redis.SAdd(ctx, "seckill:order:"+voucher, f.users["A"]).Err() != nil || resources.Redis.HSet(ctx, "seckill:accepted:"+voucher, f.users["A"], id).Err() != nil || resources.Redis.HSet(ctx, "order:reservation:"+id, "userId", f.users["A"], "voucherId", voucher, "state", Created).Err() != nil {
			t.Fatal("cannot prepare exact pending-projection fixture")
		}
		if _, err := service.Cancel(ctx, f.users["A"], id, ""); err != nil {
			t.Fatal("cannot cancel projection fixture")
		}
		if outcome, err := creator.Create(ctx, id, f.users["A"], voucher); err != nil || outcome.State != Created || outcome.View == nil || *outcome.View.OrderStatus != 4 {
			t.Fatal("old creation message reset the cancelled order")
		}
		if stock, _ := resources.Redis.Get(ctx, "seckill:stock:"+voucher).Int(); stock != 9 {
			t.Fatal("cancellation prematurely performed Redis inventory compensation")
		}
		if member, _ := resources.Redis.SIsMember(ctx, "seckill:order:"+voucher, f.users["A"]).Result(); !member {
			t.Fatal("cancellation prematurely released the Redis eligibility marker")
		}
		if mapped, _ := resources.Redis.HGet(ctx, "seckill:accepted:"+voucher, f.users["A"]).Result(); mapped != id {
			t.Fatal("cancellation prematurely deleted the stable accepted mapping")
		}
		if recovered, err := service.Seckill(ctx, f.users["A"], voucher); err != nil || recovered != id {
			t.Fatal("pending compensation must not pretend a fresh Redis purchase is available")
		}
		newID := f.nextID()
		if outcome, err := creator.Create(ctx, newID, f.users["A"], voucher); err != nil || outcome.State != Created {
			t.Fatal("cancelled active_flag NULL must permit a later correctly admitted DB order")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE user_id=? AND voucher_id=? AND active_flag=1", []any{f.users["A"], voucher}, 1)
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=? AND delivery_status='PENDING'", []any{"ORDER:" + id + ":1:CHANGED"}, 1)
	})
	t.Run("invalid_versions_stop_due_and_manual_cancellation_without_partial_restore", func(t *testing.T) {
		for _, system := range []bool{false, true} {
			for _, version := range []int{-1, math.MaxInt32} {
				voucher, id := newOrder(t)
				f.exec(t, "UPDATE tb_voucher_order SET version=?,pay_deadline_at=NOW() WHERE id=?", version, id)
				var err error
				if system {
					_, err = service.CancelExpired(ctx, id)
				} else {
					_, err = service.Cancel(ctx, f.users["A"], id, "")
				}
				if !errors.Is(err, ErrUnsafe) {
					t.Fatal("unsafe cancellation version was incremented")
				}
				f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
				f.count(t, "SELECT status FROM tb_voucher_order WHERE id=?", []any{id}, 1)
			}
		}
	})
	t.Run("expiry_rechecks_deadline_after_waiting_for_current_inventory_lock", func(t *testing.T) {
		for _, becomesDue := range []bool{false, true} {
			voucher, id := newOrder(t)
			initial := "UPDATE tb_voucher_order SET pay_deadline_at=NOW() WHERE id=?"
			updated := "UPDATE tb_voucher_order SET pay_deadline_at=DATE_ADD(NOW(),INTERVAL 1 HOUR) WHERE id=?"
			if becomesDue {
				initial, updated = updated, initial
			}
			f.exec(t, initial, id)
			blocker, err := resources.SQL.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal("cannot hold inventory row for expiry window")
			}
			var stock int
			if blocker.QueryRowContext(ctx, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=? FOR UPDATE", voucher).Scan(&stock) != nil {
				_ = blocker.Rollback()
				t.Fatal("cannot lock exact expiry inventory")
			}
			func() {
				defer blocker.Rollback()
				arrived, done := make(chan struct{}, 1), make(chan struct{})
				callback := "go031_expiry_inventory_window"
				if err := resources.ORM.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Context.Value(faultKey{}) == "expire-wait" && strings.Contains(tx.Statement.SQL.String(), "FROM tb_seckill_voucher WHERE voucher_id=? FOR UPDATE") {
						select {
						case arrived <- struct{}{}:
						default:
						}
					}
				}); err != nil {
					t.Fatal("cannot observe expiry current read")
				}
				defer resources.ORM.Callback().Row().Remove(callback)
				waitCtx, cancel := context.WithTimeout(context.WithValue(ctx, faultKey{}, "expire-wait"), 5*time.Second)
				defer cancel()
				type expiryResult struct {
					changed bool
					err     error
				}
				result := make(chan expiryResult, 1)
				go func() {
					defer close(done)
					changed, err := service.CancelExpired(waitCtx, id)
					result <- expiryResult{changed, err}
				}()
				defer func() {
					_ = blocker.Rollback()
					cancel()
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("expiry did not stop before fixture cleanup")
					}
				}()
				select {
				case <-arrived:
				case <-time.After(3 * time.Second):
					t.Fatal("expiry did not reach its current inventory SQL")
				}
				if _, err := blocker.ExecContext(ctx, updated, id); err != nil {
					t.Fatal("cannot change the locked deadline fixture")
				}
				if err := blocker.Commit(); err != nil {
					t.Fatal("cannot release changed deadline fixture")
				}
				select {
				case got := <-result:
					if got.err != nil || got.changed != becomesDue {
						t.Fatal("expiry used stale deadline after row-lock wait")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("expiry did not resume after current lock release")
				}
			}()
			if becomesDue {
				assertCancelled(t, voucher, id, "PAY_TIMEOUT", "0", "SYSTEM")
			} else {
				f.count(t, "SELECT status FROM tb_voucher_order WHERE id=?", []any{id}, 1)
				f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
			}
		}
	})
}
