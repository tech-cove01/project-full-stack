package order

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/activitypub"
	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/orderadmission"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

type faultKey struct{}

// The suite owns exact random rows, stream entries and Redis keys. It does not
// run a consumer, perform DDL, reset counters, delete a group or flush Redis.
// Sequence increments and AUTO_INCREMENT high-water marks remain monotonic.
func TestIntegrationOrder(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	resources, err := database.Open(ctx, cfg)
	cancel()
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("dependency cleanup failed")
		}
	})
	conn, err := resources.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var locked int
	if err = conn.QueryRowContext(context.Background(), "SELECT GET_LOCK('go030_stream_integration',30)").Scan(&locked); err != nil || locked != 1 {
		_ = conn.Close()
		t.Fatal("another Stream integration owns this fixture window")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK('go030_stream_integration')")
		_ = conn.Close()
	})
	creator := NewCreator(resources.ORM, 15*time.Minute)
	service := NewService(resources.ORM, resources.Redis, 15*time.Minute)
	identity := auth.NewService(resources.ORM, resources.Redis, "development")
	router := httpapi.NewRouter(resources, time.Second, "order-integration", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) { auth.Register(routes, identity); Register(routes, service, identity) })
	f := newFixture(t, resources, router)
	ctx = context.Background()
	t.Run("creation_commits_db_clock_snapshots_inventory_audit_and_event_even_after_offline", func(t *testing.T) {
		voucher := f.activity(t, 10)
		id := f.nextID()
		f.exec(t, "UPDATE tb_seckill_voucher SET publish_status='OFFLINE',publish_version=2,end_time=DATE_SUB(NOW(),INTERVAL 1 HOUR),redeem_end_time=DATE_ADD(NOW(),INTERVAL 5 MINUTE) WHERE voucher_id=?", voucher)
		outcome, err := creator.Create(ctx, id, f.users["A"], voucher)
		if err != nil || outcome.State != Created || outcome.View == nil || outcome.View.OrderStatus == nil || *outcome.View.OrderStatus != 1 || *outcome.View.PayAmount != "1900" {
			t.Fatal("accepted messages must commit original snapshots even after offline/sales end")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND status=1 AND version=0 AND pay_type=1 AND pay_amount=1900 AND pay_deadline_at=redeem_end_time AND TIMESTAMPDIFF(SECOND,create_time,NOW()) BETWEEN 0 AND 10", []any{id}, 1)
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CREATE' AND operator_user_id=? AND operator_type='USER'", []any{id, f.users["A"]}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=? AND aggregate_type='ORDER' AND aggregate_version=0 AND event_type='ORDER_CHANGED' AND delivery_status='PENDING'", []any{"ORDER:" + id + ":0:CHANGED"}, 1)
		deadline := *outcome.View.PayDeadlineAt
		f.exec(t, "UPDATE tb_voucher SET pay_value=2500 WHERE id=?", voucher)
		f.exec(t, "UPDATE tb_seckill_voucher SET redeem_end_time=DATE_ADD(NOW(),INTERVAL 2 DAY) WHERE voucher_id=?", voucher)
		view, err := service.Get(ctx, f.users["A"], id)
		if err != nil || *view.PayAmount != "1900" || *view.PayDeadlineAt != deadline {
			t.Fatal("later catalog changes replaced committed snapshots")
		}
	})
	t.Run("same_message_replay_reads_current_terminal_state_without_rewriting_order_or_stock", func(t *testing.T) {
		voucher := f.activity(t, 10)
		id := f.nextID()
		if outcome, err := creator.Create(ctx, id, f.users["A"], voucher); err != nil || outcome.State != Created {
			t.Fatal("cannot create replay fixture")
		}
		for _, status := range []int{1, 2, 3, 4, 6} {
			f.exec(t, "UPDATE tb_voucher_order SET status=? WHERE id=?", status, id)
			outcome, err := creator.Create(ctx, id, f.users["A"], voucher)
			if err != nil || outcome.State != Created || outcome.View == nil || *outcome.View.OrderStatus != status {
				t.Fatal("redelivery reset a current business state")
			}
		}
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CREATE'", []any{id}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=?", []any{"ORDER:" + id + ":0:CHANGED"}, 1)
	})
	t.Run("same_id_concurrency_and_active_unique_have_one_stock_effect_and_durable_rejection", func(t *testing.T) {
		voucher := f.activity(t, 10)
		id := f.nextID()
		const requests = 8
		start := make(chan struct{})
		var wait sync.WaitGroup
		outcomes := make([]Outcome, requests)
		errs := make([]error, requests)
		for i := range requests {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				<-start
				outcomes[i], errs[i] = creator.Create(ctx, id, f.users["A"], voucher)
			}(i)
		}
		close(start)
		wait.Wait()
		for i := range requests {
			if errs[i] != nil {
				outcomes[i], errs[i] = creator.Create(ctx, id, f.users["A"], voucher)
			}
			if errs[i] != nil || outcomes[i].State != Created {
				t.Fatal("same-message retry did not return the single committed current order")
			}
		}
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
		rejectedID := f.nextID()
		rejected, err := creator.Create(ctx, rejectedID, f.users["A"], voucher)
		if err != nil || rejected.State != Failed {
			t.Fatal("another active order must produce a durable reservation rejection")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", []any{rejectedID}, 0)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=? AND aggregate_type='RESERVATION' AND event_type='RESERVATION_REJECTED' AND JSON_EXTRACT(payload,'$.releasePurchase')=false AND delivery_status='PENDING'", []any{rejectionID(rejectedID)}, 1)
		f.exec(t, "UPDATE tb_voucher_order SET status=4 WHERE id=?", id)
		if outcome, err := creator.Create(ctx, f.nextID(), f.users["A"], voucher); err != nil || outcome.State != Created {
			t.Fatal("cancelled active_flag NULL must allow a new order")
		}
	})
	t.Run("sold_out_rejection_is_persistent_idempotent_private_and_not_an_inventory_compensation", func(t *testing.T) {
		voucher := f.activity(t, 0)
		id := f.nextID()
		for range 2 {
			outcome, err := creator.Create(ctx, id, f.users["A"], voucher)
			if err != nil || outcome.State != Failed {
				t.Fatal("stock rejection must remain the same durable failure")
			}
		}
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 0)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE event_id=? AND JSON_EXTRACT(payload,'$.releasePurchase')=true AND JSON_EXTRACT(payload,'$.stockDelta')=1 AND delivery_status='PENDING'", []any{rejectionID(id)}, 1)
		view, err := service.Get(ctx, f.users["A"], id)
		if err != nil || view.State != Failed || view.OrderStatus != nil || view.PayAmount != nil || view.PayDeadlineAt != nil {
			t.Fatal("only durable rejection may expose FAILED without an order snapshot")
		}
		if _, err := service.Get(ctx, f.users["B"], id); !errors.Is(err, ErrNotFound) {
			t.Fatal("another user read the rejected reservation")
		}
	})
	t.Run("audit_or_outbox_failure_rolls_back_all_order_and_inventory_effects", func(t *testing.T) {
		name := "go030_order_injected_raw_failure"
		if err := resources.ORM.Callback().Raw().Before("gorm:raw").Register(name, func(tx *gorm.DB) {
			fault, _ := tx.Statement.Context.Value(faultKey{}).(string)
			query := tx.Statement.SQL.String()
			if (fault == "audit" && strings.Contains(query, "INSERT INTO tb_audit_log")) || (fault == "outbox" && strings.Contains(query, "INSERT INTO tb_outbox")) {
				tx.AddError(errors.New("injected owned order persistence failure"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resources.ORM.Callback().Raw().Remove(name) })
		for _, fault := range []string{"audit", "outbox"} {
			voucher := f.activity(t, 10)
			id := f.nextID()
			outcome, err := creator.Create(context.WithValue(ctx, faultKey{}, fault), id, f.users["A"], voucher)
			if err == nil || outcome.State != "" {
				t.Fatal("failed transaction must remain unknown, never a durable outcome")
			}
			f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 10)
			f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", []any{id}, 0)
			f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=?", []any{id}, 0)
			f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=? AND aggregate_type IN ('ORDER','RESERVATION')", []any{id}, 0)
			if recovered, err := creator.Create(ctx, id, f.users["A"], voucher); err != nil || recovered.State != Created {
				t.Fatal("a rolled-back order must be retryable after persistence returns")
			}
		}
	})
	t.Run("identifier_collision_including_cross_voucher_rejection_race_never_creates_two_facts", func(t *testing.T) {
		first, second := f.activity(t, 10), f.activity(t, 0)
		id := f.nextID()
		start := make(chan struct{})
		outcomes := make([]Outcome, 2)
		errs := make([]error, 2)
		var wait sync.WaitGroup
		for i, voucher := range []string{first, second} {
			wait.Add(1)
			go func(i int, voucher string) {
				defer wait.Done()
				<-start
				outcomes[i], errs[i] = creator.Create(ctx, id, f.users["A"], voucher)
			}(i, voucher)
		}
		close(start)
		wait.Wait()
		terminal := 0
		for i := range 2 {
			if errs[i] == nil {
				terminal++
				if outcomes[i].State != Created && outcomes[i].State != Failed {
					t.Fatal("unexpected terminal state")
				}
			}
		}
		if terminal != 1 {
			t.Fatal("a shared order identifier must allow exactly one committed identity")
		}
		for i, voucher := range []string{first, second} {
			if errs[i] != nil {
				if _, err := creator.Create(ctx, id, f.users["A"], voucher); !errors.Is(err, ErrCollision) {
					t.Fatal("the losing identity must remain a collision, not a new rejection")
				}
			}
		}
		f.count(t, "SELECT (SELECT COUNT(*) FROM tb_voucher_order WHERE id=?) + (SELECT COUNT(*) FROM tb_outbox WHERE event_id=?)", []any{id, rejectionID(id)}, 1)
		if _, err := creator.Create(ctx, id, f.users["B"], first); !errors.Is(err, ErrCollision) {
			t.Fatal("another owner's same ID must remain unknown/collision")
		}
	})
	t.Run("identifier_lock_contention_stays_unknown_and_release_failure_discards_its_connection", func(t *testing.T) {
		voucher := f.activity(t, 10)
		id := f.nextID()
		name := "go:order:" + id
		holder, err := resources.SQL.Conn(ctx)
		if err != nil {
			t.Fatal("cannot reserve owned lock holder")
		}
		defer holder.Close()
		var acquired int
		if err = holder.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&acquired); err != nil || acquired != 1 {
			t.Fatal("cannot hold the exact identifier lock")
		}
		defer holder.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", name)
		if outcome, err := creator.Create(ctx, id, f.users["A"], voucher); err == nil || outcome.State != "" {
			t.Fatal("identifier contention must remain pending, not claim a committed outcome")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", []any{id}, 0)
		var released int
		if err = holder.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", name).Scan(&released); err != nil || released != 1 {
			t.Fatal("cannot release exact held identifier")
		}
		callback := "go030_inject_identifier_release_failure"
		if err = resources.ORM.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
			if strings.Contains(tx.Statement.SQL.String(), "RELEASE_LOCK") && len(tx.Statement.Vars) == 1 && tx.Statement.Vars[0] == name {
				tx.AddError(errors.New("injected exact identifier release failure"))
			}
		}); err != nil {
			t.Fatal("cannot inject release failure")
		}
		t.Cleanup(func() { _ = resources.ORM.Callback().Row().Remove(callback) })
		outcome, createErr := creator.Create(ctx, id, f.users["A"], voucher)
		_ = resources.ORM.Callback().Row().Remove(callback)
		if createErr == nil || outcome.State != "" {
			t.Fatal("release failure must preserve an unknown response after commit")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", []any{id}, 1)
		// If the failed-release physical connection leaked into the pool, this
		// independent connection cannot obtain its lock and this assertion fails.
		if err = holder.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&acquired); err != nil || acquired != 1 {
			t.Fatal("failed RELEASE_LOCK leaked a live named lock back to the pool")
		}
		if err = holder.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", name).Scan(&released); err != nil || released != 1 {
			t.Fatal("cannot release exact observed lock")
		}
		if recovered, err := creator.Create(ctx, id, f.users["A"], voucher); err != nil || recovered.State != Created {
			t.Fatal("fresh retry must return the durable order after uncertain lock release")
		}
		f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", []any{voucher}, 9)
	})
	t.Run("db_facts_override_forged_redis_terminal_states_and_pending_is_explicit_null", func(t *testing.T) {
		voucher := f.activity(t, 10)
		id := f.nextID()
		if _, err := creator.Create(ctx, id, f.users["A"], voucher); err != nil {
			t.Fatal(err)
		}
		if resources.Redis.HSet(ctx, "order:reservation:"+id, "userId", f.users["B"], "voucherId", voucher, "state", Failed).Err() != nil {
			t.Fatal("cannot arrange owned cache inconsistency")
		}
		view, err := service.Get(ctx, f.users["A"], id)
		if err != nil || view.State != Created || *view.OrderStatus != 1 {
			t.Fatal("Redis FAILED overrode an existing DB order")
		}
		if _, err := service.Get(ctx, f.users["B"], id); !errors.Is(err, ErrNotFound) {
			t.Fatal("forged reservation ownership exposed another person's order")
		}
		if resources.Redis.HSet(ctx, "seckill:accepted:"+voucher, f.users["B"], id).Err() != nil {
			t.Fatal("cannot arrange mismatched accepted index")
		}
		if _, err := service.Seckill(ctx, f.users["B"], voucher); !errors.Is(err, ErrCollision) {
			t.Fatal("accepted cache cannot return another person's committed order identifier")
		}
		for _, state := range []string{Failed, Created, Processing} {
			pending := f.nextID()
			if resources.Redis.HSet(ctx, "order:reservation:"+pending, "userId", f.users["A"], "voucherId", voucher, "state", state).Err() != nil {
				t.Fatal("cannot arrange pending reservation")
			}
			reply := f.call(t, "A", http.MethodGet, "/voucher-order/"+pending, nil)
			var fields map[string]any
			if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &fields) != nil || fields["state"] != Processing {
				t.Fatal("unproven Redis terminal state must remain PROCESSING")
			}
			if status, present := fields["orderStatus"]; !present || status != nil {
				t.Fatal("pending orderStatus must be explicit null")
			}
			for _, absent := range []string{"payAmount", "payDeadlineAt", "redeemToken"} {
				if _, exists := fields[absent]; exists {
					t.Fatal("pending reservation fabricated a committed snapshot")
				}
			}
		}
	})
	t.Run("get_returns_the_owned_order_committed_between_its_two_db_reads", func(t *testing.T) {
		voucher, id := f.activity(t, 10), f.nextID()
		arrived, resume := make(chan struct{}, 1), make(chan struct{})
		var release sync.Once
		unblock := func() { release.Do(func() { close(resume) }) }
		defer unblock()
		callback := "go030_get_owned_query_commit_window"
		if err := resources.ORM.Callback().Row().After("gorm:row").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Context.Value(faultKey{}) != "get-commit-race" || !strings.Contains(tx.Statement.SQL.String(), "FROM tb_voucher_order WHERE id=? AND user_id=?") {
				return
			}
			select {
			case arrived <- struct{}{}:
			default:
			}
			select {
			case <-resume:
			case <-tx.Statement.Context.Done():
				tx.AddError(tx.Statement.Context.Err())
			}
		}); err != nil {
			t.Fatal("cannot observe first owned-order SQL boundary")
		}
		t.Cleanup(func() { _ = resources.ORM.Callback().Row().Remove(callback) })
		type getResult struct {
			view View
			err  error
		}
		result, done := make(chan getResult, 1), make(chan struct{})
		getCtx, cancel := context.WithTimeout(context.WithValue(ctx, faultKey{}, "get-commit-race"), 5*time.Second)
		defer cancel()
		go func() {
			defer close(done)
			view, err := service.Get(getCtx, f.users["A"], id)
			result <- getResult{view, err}
		}()
		t.Cleanup(func() {
			unblock()
			cancel()
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Error("controlled Get did not exit before fixture cleanup")
			}
		})
		select {
		case <-arrived:
		case <-time.After(3 * time.Second):
			t.Fatal("Get never reached its first owned-order query")
		}
		if outcome, err := creator.Create(ctx, id, f.users["A"], voucher); err != nil || outcome.State != Created {
			t.Fatal("concurrent worker fixture did not commit its owned order")
		}
		unblock()
		select {
		case got := <-result:
			if got.err != nil || got.view.State != Created || got.view.ID != id || got.view.OrderStatus == nil || *got.view.OrderStatus != 1 {
				t.Fatalf("order committed between Get reads must be CREATED, got state=%q err=%v", got.view.State, got.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Get did not resume after the controlled commit")
		}
	})
	t.Run("accepted_recovery_and_lua_reuse_reject_foreign_pending_or_committed_bindings", func(t *testing.T) {
		for _, test := range []struct {
			name             string
			committed, reuse bool
			wrongType        bool
		}{
			{"recover_foreign_pending", false, false, false}, {"reuse_foreign_committed", true, true, false},
			{"reuse_foreign_pending", false, true, false}, {"recover_wrong_type_reservation", false, false, true},
		} {
			t.Run(test.name, func(t *testing.T) {
				voucher, foreign := f.activity(t, 10), f.nextID()
				if test.committed {
					if outcome, err := creator.Create(ctx, foreign, f.users["B"], voucher); err != nil || outcome.State != Created {
						t.Fatal("cannot prepare the foreign committed identity")
					}
				}
				key := "order:reservation:" + foreign
				if test.wrongType {
					if resources.Redis.Set(ctx, key, "wrong-type", 0).Err() != nil {
						t.Fatal("cannot arrange wrong-type reservation")
					}
				} else if resources.Redis.HSet(ctx, key, "userId", f.users["B"], "voucherId", voucher, "state", Processing).Err() != nil {
					t.Fatal("cannot arrange foreign pending reservation")
				}
				setMap := func() error {
					return resources.Redis.HSet(ctx, "seckill:accepted:"+voucher, f.users["A"], foreign).Err()
				}
				isolated := NewService(resources.ORM, resources.Redis, 15*time.Minute)
				if test.reuse {
					begin, end := f.activityWindow(t, voucher)
					gate := activitypub.NewGate(resources.Redis)
					if gate.Prepare(ctx, voucher, 1, 10, begin, end) != nil || gate.Open(ctx, voucher, 1) != nil {
						t.Fatal("cannot prepare controlled Lua reuse")
					}
					isolated.admission = interleavedAdmission{admission: isolated.admission, beforeReserve: setMap}
				} else if setMap() != nil {
					t.Fatal("cannot arrange recovery index")
				}
				returned, err := isolated.Seckill(ctx, f.users["A"], voucher)
				if err == nil || returned != "" {
					t.Fatalf("foreign or malformed accepted binding leaked order identifier %q", returned)
				}
				if !test.wrongType && !errors.Is(err, ErrCollision) {
					t.Fatalf("foreign accepted identity must remain an unknown collision, got %v", err)
				}
			})
		}
	})
	t.Run("owned_list_has_stable_tie_order_paging_empty_total_and_no_tokens", func(t *testing.T) {
		// A separate user isolates pagination from the other owned order cases.
		ids := []string{}
		for range 3 {
			voucher := f.activity(t, 10)
			id := f.nextID()
			ids = append(ids, id)
			if _, err := creator.Create(ctx, id, f.users["C"], voucher); err != nil {
				t.Fatal(err)
			}
			f.exec(t, "UPDATE tb_voucher_order SET status=2,redeem_token=?,create_time='2026-10-26 10:00:00' WHERE id=?", "fixture-token-"+id, id)
		}
		for page := 1; page <= 3; page++ {
			reply := f.call(t, "C", http.MethodGet, fmt.Sprintf("/voucher-order?current=%d&pageSize=1", page), nil)
			var rows []View
			if reply.Code != 200 || !reply.Success || reply.Total == nil || *reply.Total != "3" || json.Unmarshal(reply.Data, &rows) != nil || len(rows) != 1 || rows[0].ID != ids[3-page] || rows[0].RedeemToken != nil {
				t.Fatal("owned pagination lost deterministic ordering, exact total or token privacy")
			}
		}
		reply := f.call(t, "C", http.MethodGet, "/voucher-order?current=18446744073709551615&pageSize=50", nil)
		var rows []View
		if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &rows) != nil || rows == nil || len(rows) != 0 || reply.Total == nil || *reply.Total != "3" {
			t.Fatal("far pagination must return [] and the exact total")
		}
		view, err := service.Get(ctx, f.users["C"], ids[0])
		if err != nil || view.RedeemToken == nil {
			t.Fatal("only the owner's paid detail may include its token")
		}
	})
	t.Run("seckill_reuses_existing_db_or_accepted_id_and_reserves_new_request_once", func(t *testing.T) {
		voucher := f.activity(t, 10)
		id := f.nextID()
		if _, err := creator.Create(ctx, id, f.users["A"], voucher); err != nil {
			t.Fatal(err)
		}
		f.exec(t, "UPDATE tb_seckill_voucher SET publish_status='OFFLINE',publish_version=2 WHERE voucher_id=?", voucher)
		withoutRedis := NewService(resources.ORM, nil, 15*time.Minute)
		if reused, err := withoutRedis.Seckill(ctx, f.users["A"], voucher); err != nil || reused != id {
			t.Fatal("DB active order must be recoverable before touching Redis/gate")
		}
		reservedVoucher := f.activity(t, 10)
		reserved := f.nextID()
		if resources.Redis.HSet(ctx, "seckill:accepted:"+reservedVoucher, f.users["A"], reserved).Err() != nil {
			t.Fatal("cannot arrange accepted index")
		}
		f.exec(t, "UPDATE tb_seckill_voucher SET publish_status='OFFLINE',publish_version=2 WHERE voucher_id=?", reservedVoucher)
		if reused, err := service.Seckill(ctx, f.users["A"], reservedVoucher); err != nil || reused != reserved {
			t.Fatal("accepted ID must remain recoverable after offline")
		}
		fresh := f.activity(t, 10)
		begin, end := f.activityWindow(t, fresh)
		gate := activitypub.NewGate(resources.Redis)
		if gate.Prepare(ctx, fresh, 1, 10, begin, end) != nil || gate.Open(ctx, fresh, 1) != nil {
			t.Fatal("cannot prepare admission fixture")
		}
		accepted, err := service.Seckill(ctx, f.users["A"], fresh)
		if err != nil || accepted == "" {
			t.Fatal("new seckill request was not accepted")
		}
		f.track(accepted)
		if repeated, err := service.Seckill(ctx, f.users["A"], fresh); err != nil || repeated != accepted {
			t.Fatal("same request reserved a second order")
		}
		if stock, _ := resources.Redis.Get(ctx, "seckill:stock:"+fresh).Int(); stock != 9 {
			t.Fatal("retry deducted Redis stock again")
		}
		messages, err := resources.Redis.XRange(ctx, "stream.orders", "-", "+").Result()
		if err != nil {
			t.Fatal("cannot inspect owned Stream entry")
		}
		count := 0
		for _, message := range messages {
			if fmt.Sprint(message.Values["voucherId"]) == fresh {
				count++
				if fmt.Sprint(message.Values["id"]) != accepted || fmt.Sprint(message.Values["userId"]) != f.users["A"] {
					t.Fatal("Stream envelope did not preserve admitted identity")
				}
			}
		}
		if count != 1 {
			t.Fatal("idempotent admission appended more than one Stream entry")
		}
	})
}

type reply struct {
	Code    int
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Total   *string         `json:"total"`
}

// This wrapper creates the real race after Recover has returned no mapping and
// before the atomic Lua Reserve observes a newly inserted accepted index.
type interleavedAdmission struct {
	admission
	beforeReserve func() error
}

func (a interleavedAdmission) Reserve(ctx context.Context, voucher, user, id string, now, begin, end time.Time, version int) (orderadmission.Result, error) {
	if err := a.beforeReserve(); err != nil {
		return orderadmission.Result{}, err
	}
	return a.admission.Reserve(ctx, voucher, user, id, now, begin, end, version)
}

type fixture struct {
	resources             *database.Resources
	router                http.Handler
	base, serial          uint64
	merchant, shop        string
	users, phones, tokens map[string]string
	vouchers, ids         map[string]bool
	mu                    sync.Mutex
}

func newFixture(t *testing.T, resources *database.Resources, router http.Handler) *fixture {
	t.Helper()
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	base := uint64(9200000000000000) + (binary.BigEndian.Uint64(entropy[:])%1000000000000)*32
	f := &fixture{resources: resources, router: router, base: base, merchant: decimal(base), shop: decimal(base), users: map[string]string{}, phones: map[string]string{}, tokens: map[string]string{}, vouchers: map[string]bool{}, ids: map[string]bool{}}
	t.Cleanup(func() { f.cleanup(t) })
	f.exec(t, "INSERT INTO tb_merchant(id,name) VALUES(?,'owned GO030 merchant')", f.merchant)
	f.exec(t, "INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(?,?,'owned GO030 shop',1,'owned fixture',120,30)", f.shop, f.merchant)
	for i, name := range []string{"A", "B", "C"} {
		id := decimal(base + uint64(i))
		phone := fmt.Sprintf("138%08d", (base/32+uint64(i))%100000000)
		f.exec(t, "INSERT INTO tb_user(id,phone,nick_name) VALUES(?,?,?)", id, phone, "GO030"+name)
		f.users[name], f.phones[name] = id, phone
	}
	for name, phone := range f.phones {
		response := f.call(t, "", http.MethodPost, "/user/code?phone="+phone, nil)
		var code auth.DevelopmentCode
		if response.Code != 200 || !response.Success || json.Unmarshal(response.Data, &code) != nil || code.Code == "" {
			t.Fatal("fixture code failed")
		}
		response = f.call(t, "", http.MethodPost, "/user/login", auth.LoginForm{Phone: phone, Code: code.Code})
		var token string
		if response.Code != 200 || !response.Success || json.Unmarshal(response.Data, &token) != nil || token == "" {
			t.Fatal("fixture real login failed")
		}
		f.tokens[name] = token
	}
	return f
}
func (f *fixture) window() (time.Time, time.Time) {
	now := time.Now().In(businessZone).Truncate(time.Second)
	return now.Add(-time.Hour), now.Add(time.Hour)
}

func (f *fixture) activityWindow(t *testing.T, voucher string) (time.Time, time.Time) {
	t.Helper()
	var begin, end time.Time
	if err := f.resources.SQL.QueryRow("SELECT begin_time,end_time FROM tb_seckill_voucher WHERE voucher_id=?", voucher).Scan(&begin, &end); err != nil {
		t.Fatal("cannot read exact fixture publication window")
	}
	return begin, end
}
func (f *fixture) activity(t *testing.T, stock int) string {
	t.Helper()
	f.serial++
	id := decimal(f.base + 100 + f.serial)
	begin, end := f.window()
	f.exec(t, "INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES(?,?,'owned GO030 voucher',1900,3000,1,1)", id, f.shop)
	f.vouchers[id] = true
	f.exec(t, `INSERT INTO tb_seckill_voucher(voucher_id,merchant_id,stock,total_stock,publish_status,publish_version,begin_time,end_time,redeem_begin_time,redeem_end_time) VALUES(?,?,?,?,'ACTIVE',1,?,?,?,?)`, id, f.merchant, stock, stock, begin, end, begin, end.Add(time.Hour))
	return id
}
func (f *fixture) nextID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serial++
	id := decimal(f.base + 10000 + f.serial)
	f.ids[id] = true
	return id
}
func (f *fixture) track(id string) { f.mu.Lock(); defer f.mu.Unlock(); f.ids[id] = true }
func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.resources.SQL.ExecContext(ctx, query, args...); err != nil {
		t.Fatal("exact fixture mutation failed", err)
	}
}
func (f *fixture) count(t *testing.T, query string, args []any, want int) {
	t.Helper()
	var got int
	if err := f.resources.SQL.QueryRow(query, args...).Scan(&got); err != nil || got != want {
		t.Fatalf("persistent invariant expected %d, observed %d (%v)", want, got, err)
	}
}
func (f *fixture) call(t *testing.T, user, method, path string, body any) reply {
	t.Helper()
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, input)
	if user != "" {
		request.Header.Set("authorization", f.tokens[user])
	}
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	var result reply
	if json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatal("invalid API response")
	}
	result.Code = response.Code
	return result
}
func (f *fixture) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	keys := []string{}
	for _, phone := range f.phones {
		keys = append(keys, "login:code:"+phone, "login:code:throttle:"+phone)
	}
	for _, token := range f.tokens {
		keys = append(keys, "login:token:"+token)
	}
	for voucher := range f.vouchers {
		accepted, _ := f.resources.Redis.HGetAll(ctx, "seckill:accepted:"+voucher).Result()
		for _, id := range accepted {
			f.ids[id] = true
		}
		for _, prefix := range []string{"seckill:gate:", "seckill:stock:", "seckill:begin:", "seckill:end:", "seckill:order:", "seckill:accepted:"} {
			keys = append(keys, prefix+voucher)
		}
	}
	if messages, err := f.resources.Redis.XRange(ctx, "stream.orders", "-", "+").Result(); err == nil {
		for _, message := range messages {
			if f.vouchers[fmt.Sprint(message.Values["voucherId"])] {
				_ = f.resources.Redis.XAck(ctx, "stream.orders", "g1", message.ID).Err()
				if f.resources.Redis.XDel(ctx, "stream.orders", message.ID).Err() != nil {
					t.Error("cannot delete owned Stream entry")
				}
			}
		}
	} else if !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Error("cannot inspect owned Stream cleanup")
	}
	for id := range f.ids {
		keys = append(keys, "order:reservation:"+id)
		for _, query := range []string{"DELETE FROM tb_outbox WHERE aggregate_id=? AND aggregate_type IN ('ORDER','RESERVATION')", "DELETE FROM tb_voucher_order WHERE id=?"} {
			if _, err := f.resources.SQL.ExecContext(ctx, query, id); err != nil {
				t.Error("cannot delete owned order/event")
			}
		}
	}
	for voucher := range f.vouchers {
		for _, query := range []string{"DELETE FROM tb_seckill_voucher WHERE voucher_id=?", "DELETE FROM tb_voucher WHERE id=?"} {
			if _, err := f.resources.SQL.ExecContext(ctx, query, voucher); err != nil {
				t.Error("cannot delete owned activity")
			}
		}
	}
	if len(keys) > 0 && f.resources.Redis.Del(ctx, keys...).Err() != nil {
		t.Error("cannot remove owned Redis keys")
	}
	for _, target := range []struct {
		query string
		arg   any
	}{{"DELETE FROM tb_audit_log WHERE merchant_id=?", f.merchant}, {"DELETE FROM tb_shop WHERE id=?", f.shop}, {"DELETE FROM tb_merchant WHERE id=?", f.merchant}} {
		if _, err := f.resources.SQL.ExecContext(ctx, target.query, target.arg); err != nil {
			t.Error("cannot delete owned merchant fixture")
		}
	}
	for _, id := range f.users {
		if _, err := f.resources.SQL.ExecContext(ctx, "DELETE FROM tb_user WHERE id=?", id); err != nil {
			t.Error("cannot delete owned user fixture")
		}
	}
}
