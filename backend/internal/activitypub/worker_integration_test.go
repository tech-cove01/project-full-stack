package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/outbox"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

type publicationFixture struct {
	resources  *database.Resources
	id         string
	begin, end time.Time
}

func newPublicationFixture(t *testing.T, resources *database.Resources) *publicationFixture {
	t.Helper()
	id := ownedGateID(t, resources.Redis)
	fixture := &publicationFixture{resources: resources, id: id, begin: time.Now().In(time.FixedZone("Shanghai", 8*3600)).Truncate(time.Second).Add(-time.Minute)}
	fixture.end = fixture.begin.Add(time.Hour)
	var count int64
	if resources.ORM.Raw("SELECT COUNT(*) FROM tb_voucher WHERE id = ?", id).Scan(&count).Error != nil || count != 0 {
		t.Fatal("publication fixture ID already exists")
	}
	fixture.exec(t, `INSERT INTO tb_voucher (id, shop_id, title, pay_value, actual_value, type, status) VALUES (?, ?, 'Go publication fixture', 100, 200, 1, 1)`, id, id)
	t.Cleanup(func() {
		for _, statement := range []string{
			"DELETE FROM tb_outbox WHERE aggregate_type = 'ACTIVITY' AND aggregate_id = ?",
			"DELETE FROM tb_audit_log WHERE resource_id = ? AND action = 'ACTIVITY_ACTIVE'",
			"DELETE FROM tb_seckill_voucher WHERE voucher_id = ?", "DELETE FROM tb_voucher WHERE id = ?",
		} {
			if resources.ORM.Exec(statement, id).Error != nil {
				t.Error("cannot delete exact publication fixture rows")
			}
		}
	})
	fixture.exec(t, `INSERT INTO tb_seckill_voucher
		(voucher_id, merchant_id, stock, total_stock, publish_status, publish_version, begin_time, end_time, redeem_begin_time, redeem_end_time)
		VALUES (?, ?, 10, 10, 'INITIALIZING', 0, ?, ?, ?, ?)`, id, id, fixture.begin, fixture.end, fixture.begin, fixture.end.Add(time.Hour))
	return fixture
}

func (fixture *publicationFixture) exec(t *testing.T, statement string, arguments ...any) {
	t.Helper()
	if fixture.resources.ORM.Exec(statement, arguments...).Error != nil {
		t.Fatal("cannot mutate exact publication fixture")
	}
}

func (fixture *publicationFixture) event(action string, version int) outbox.Event {
	payload, _ := json.Marshal(map[string]any{"activityId": fixture.id, "version": version})
	return outbox.Event{EventID: fmt.Sprintf("ACTIVITY:%s:%d:%s", fixture.id, version, action), AggregateType: "ACTIVITY", AggregateID: fixture.id,
		AggregateVersion: version, EventType: "ACTIVITY_" + action, Payload: payload}
}

func (fixture *publicationFixture) requireState(t *testing.T, status string, version int, auditCount, openCount int64) {
	t.Helper()
	var row struct {
		PublishStatus  string
		PublishVersion int
	}
	if fixture.resources.ORM.Raw("SELECT publish_status, publish_version FROM tb_seckill_voucher WHERE voucher_id = ?", fixture.id).Scan(&row).Error != nil || row.PublishStatus != status || row.PublishVersion != version {
		t.Fatal("publication status/version differs from the durable expected state")
	}
	var audits, opens int64
	if fixture.resources.ORM.Raw("SELECT COUNT(*) FROM tb_audit_log WHERE resource_id = ? AND action = 'ACTIVITY_ACTIVE'", fixture.id).Scan(&audits).Error != nil || audits != auditCount {
		t.Fatal("system audit count differs from committed publication transitions")
	}
	if fixture.resources.ORM.Raw("SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type = 'ACTIVITY' AND aggregate_id = ? AND event_type = 'ACTIVITY_OPEN'", fixture.id).Scan(&opens).Error != nil || opens != openCount {
		t.Fatal("OPEN event count differs from committed publication transitions")
	}
}

func TestIntegrationPublication(t *testing.T) {
	resources := integrationResources(t, true)
	ctx, worker := context.Background(), NewWorker(resources.ORM, resources.Redis)
	t.Run("prepare_commits_active_audit_and_event_before_opening", func(t *testing.T) {
		fixture := newPublicationFixture(t, resources)
		var observed atomic.Bool
		callback := "go021_check_before_open_enqueue"
		if err := resources.ORM.Callback().Raw().Before("gorm:raw").Register(callback, func(tx *gorm.DB) {
			if strings.Contains(tx.Statement.SQL.String(), "INSERT INTO tb_outbox") {
				observed.Store(true)
				if ready, err := worker.gate.Ready(ctx, fixture.id, 1); err != nil || ready {
					tx.AddError(errors.New("gate opened before publication commit"))
				}
				fixture.requireState(t, "INITIALIZING", 0, 0, 0)
			}
		}); err != nil {
			t.Fatal("cannot register transaction observation")
		}
		err := worker.dispatch(ctx, fixture.event("PREPARE", 0))
		_ = resources.ORM.Callback().Raw().Remove(callback)
		if err != nil || !observed.Load() {
			t.Fatal("preparation failed or did not reach the observed commit boundary")
		}
		fixture.requireState(t, "ACTIVE", 1, 1, 1)
		if ready, _ := worker.gate.Ready(ctx, fixture.id, 1); ready {
			t.Fatal("PREPARE delivery opened gate without committed OPEN delivery")
		}
		if worker.dispatch(ctx, fixture.event("OPEN", 1)) != nil {
			t.Fatal("committed OPEN failed")
		}
		if ready, _ := worker.gate.Ready(ctx, fixture.id, 1); !ready {
			t.Fatal("committed activity did not open")
		}
		if resources.Redis.DecrBy(ctx, gateKeys(fixture.id)[1], 3).Err() != nil || worker.dispatch(ctx, fixture.event("PREPARE", 0)) != nil || worker.dispatch(ctx, fixture.event("OPEN", 1)) != nil {
			t.Fatal("publication replay failed")
		}
		if value, _ := resources.Redis.Get(ctx, gateKeys(fixture.id)[1]).Int(); value != 7 {
			t.Fatal("publication replay reset inventory")
		}
		fixture.requireState(t, "ACTIVE", 1, 1, 1)
	})
	t.Run("audit_failure_rolls_back_active_and_outbox_while_gate_stays_closed", func(t *testing.T) {
		fixture := newPublicationFixture(t, resources)
		callback := "go021_fail_system_audit"
		if err := resources.ORM.Callback().Raw().Before("gorm:raw").Register(callback, func(tx *gorm.DB) {
			if strings.Contains(tx.Statement.SQL.String(), "INSERT INTO tb_audit_log") {
				tx.AddError(errors.New("injected audit failure"))
			}
		}); err != nil {
			t.Fatal("cannot register audit failure")
		}
		err := worker.dispatch(ctx, fixture.event("PREPARE", 0))
		_ = resources.ORM.Callback().Raw().Remove(callback)
		if err == nil {
			t.Fatal("audit failure must abort publication transaction")
		}
		fixture.requireState(t, "INITIALIZING", 0, 0, 0)
		if ready, _ := worker.gate.Ready(ctx, fixture.id, 1); ready {
			t.Fatal("rolled-back publication admitted reservations")
		}
		if worker.dispatch(ctx, fixture.event("PREPARE", 0)) != nil {
			t.Fatal("failed publication must retry without resetting preparation")
		}
		fixture.requireState(t, "ACTIVE", 1, 1, 1)
	})
	t.Run("outbox_failure_rolls_back_active_and_audit_while_gate_stays_closed", func(t *testing.T) {
		fixture := newPublicationFixture(t, resources)
		callback := "go021_fail_open_outbox"
		if err := resources.ORM.Callback().Raw().Before("gorm:raw").Register(callback, func(tx *gorm.DB) {
			if strings.Contains(tx.Statement.SQL.String(), "INSERT INTO tb_outbox") {
				tx.AddError(errors.New("injected outbox failure"))
			}
		}); err != nil {
			t.Fatal("cannot register outbox failure")
		}
		err := worker.dispatch(ctx, fixture.event("PREPARE", 0))
		_ = resources.ORM.Callback().Raw().Remove(callback)
		if err == nil {
			t.Fatal("outbox failure must abort publication and its audit")
		}
		fixture.requireState(t, "INITIALIZING", 0, 0, 0)
		if ready, _ := worker.gate.Ready(ctx, fixture.id, 1); ready {
			t.Fatal("outbox failure opened a rolled-back publication")
		}
		if worker.dispatch(ctx, fixture.event("PREPARE", 0)) != nil {
			t.Fatal("publication must recover after outbox persistence returns")
		}
		fixture.requireState(t, "ACTIVE", 1, 1, 1)
	})
	t.Run("offline_current_row_blocks_a_waiting_old_open", func(t *testing.T) {
		fixture := newPublicationFixture(t, resources)
		if worker.dispatch(ctx, fixture.event("PREPARE", 0)) != nil {
			t.Fatal("cannot prepare offline race")
		}
		tx := resources.ORM.Begin()
		defer tx.Rollback()
		var version int
		if tx.Raw("SELECT publish_version FROM tb_seckill_voucher WHERE voucher_id = ? FOR UPDATE", fixture.id).Scan(&version).Error != nil || version != 1 {
			t.Fatal("cannot lock current activity")
		}
		if worker.gate.Close(ctx, fixture.id, 2) != nil || tx.Exec("UPDATE tb_seckill_voucher SET publish_status = 'OFFLINE', publish_version = 2 WHERE voucher_id = ?", fixture.id).Error != nil {
			t.Fatal("cannot apply synchronous offline")
		}
		result := make(chan error, 1)
		go func() { result <- worker.dispatch(ctx, fixture.event("OPEN", 1)) }()
		select {
		case <-result:
			t.Fatal("OPEN did not wait for current offline row lock")
		case <-time.After(50 * time.Millisecond):
		}
		if tx.Commit().Error != nil {
			t.Fatal("cannot commit offline")
		}
		select {
		case err := <-result:
			if err != nil {
				t.Fatal("obsolete OPEN should complete without opening")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("old OPEN did not finish after offline")
		}
		if worker.dispatch(ctx, fixture.event("CLOSE", 2)) != nil {
			t.Fatal("close evidence replay failed")
		}
		if ready, _ := worker.gate.Ready(ctx, fixture.id, 2); ready {
			t.Fatal("old OPEN reopened the offline activity")
		}
		fixture.requireState(t, "OFFLINE", 2, 1, 1)
	})
	t.Run("redis_failure_beyond_eight_attempts_remains_pending_then_recovers", func(t *testing.T) {
		// No other unowned activity deliveries may exist while exercising claim.
		var outstanding int64
		if resources.ORM.Raw(`SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type = 'ACTIVITY'
			AND event_type IN ('ACTIVITY_PREPARE','ACTIVITY_OPEN','ACTIVITY_CLOSE')
			AND delivery_status IN ('PENDING','PROCESSING')`).Scan(&outstanding).Error != nil || outstanding != 0 {
			t.Fatal("stop other activity workers and finish fixture cleanup before the claim test")
		}
		fixture := newPublicationFixture(t, resources)
		if outbox.Enqueue(ctx, resources.ORM, fixture.event("PREPARE", 0)) != nil {
			t.Fatal("cannot queue owned publication")
		}
		unavailable := redis.NewClient(&redis.Options{Addr: resources.Redis.Options().Addr})
		_ = unavailable.Close()
		failing := NewWorker(resources.ORM, unavailable)
		for attempt := 1; attempt <= 10; attempt++ {
			fixture.exec(t, "UPDATE tb_outbox SET next_attempt_at = NOW(3) WHERE event_id = ?", fixture.event("PREPARE", 0).EventID)
			if processed, err := failing.Poll(ctx); !processed || err == nil {
				t.Fatal("Redis failure was not durably retried")
			}
			var delivery struct {
				DeliveryStatus string
				Attempts       int
				LastError      string
			}
			if resources.ORM.Raw("SELECT delivery_status, attempts, last_error FROM tb_outbox WHERE event_id = ?", fixture.event("PREPARE", 0).EventID).Scan(&delivery).Error != nil || delivery.DeliveryStatus != "PENDING" || delivery.Attempts != attempt || delivery.LastError == "" {
				t.Fatal("transient Redis failure must stay PENDING beyond eight attempts")
			}
		}
		fixture.requireState(t, "INITIALIZING", 0, 0, 0)
		fixture.exec(t, "UPDATE tb_outbox SET next_attempt_at = NOW(3) WHERE event_id = ?", fixture.event("PREPARE", 0).EventID)
		if processed, err := worker.Poll(ctx); !processed || err != nil {
			t.Fatal("publication did not recover after Redis returned")
		}
		if processed, err := worker.Poll(ctx); !processed || err != nil {
			t.Fatal("OPEN did not recover after Redis returned")
		}
		if ready, _ := worker.gate.Ready(ctx, fixture.id, 1); !ready {
			t.Fatal("recovered event did not open committed activity")
		}
	})
}
