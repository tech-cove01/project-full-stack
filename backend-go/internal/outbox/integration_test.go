package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func TestIntegrationOutbox(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx := context.Background()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	resources, err := database.Open(startup, cfg)
	cancel()
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("cannot close integration dependencies")
		}
	})
	lockCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	connection, err := resources.SQL.Conn(lockCtx)
	if err != nil {
		t.Fatal("cannot reserve integration lock connection")
	}
	var acquired int
	if connection.QueryRowContext(lockCtx, "SELECT GET_LOCK('go021_outbox_integration', 30)").Scan(&acquired) != nil || acquired != 1 {
		_ = connection.Close()
		t.Fatal("outbox integration fixture window is busy")
	}
	t.Cleanup(func() {
		var released int
		if connection.QueryRowContext(ctx, "SELECT RELEASE_LOCK('go021_outbox_integration')").Scan(&released) != nil || released != 1 {
			t.Error("cannot release integration lock")
		}
		_ = connection.Close()
	})
	var pending int64
	if resources.ORM.Raw(`SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type = 'ACTIVITY'
		AND event_type IN ('ACTIVITY_PREPARE', 'ACTIVITY_OPEN', 'ACTIVITY_CLOSE')
		AND delivery_status IN ('PENDING', 'PROCESSING')`).Scan(&pending).Error != nil || pending != 0 {
		t.Fatal("stop other consumers and clean owned activity fixtures before outbox integration")
	}
	repository := NewRepository(resources.ORM)
	fixtureEvent := func(t *testing.T, action string) Event {
		t.Helper()
		var entropy [12]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			t.Fatal("cannot allocate outbox fixture")
		}
		id := "go021_" + hex.EncodeToString(entropy[:])
		event := Event{EventID: id, AggregateType: "ACTIVITY", AggregateID: id, AggregateVersion: 1,
			EventType: action, Payload: json.RawMessage(`{"id":"9007199254740993","version":1}`)}
		t.Cleanup(func() {
			if resources.ORM.Exec("DELETE FROM tb_outbox WHERE event_id = ?", event.EventID).Error != nil {
				t.Error("cannot delete exact outbox fixture event")
			}
			var remaining int64
			if resources.ORM.Raw("SELECT COUNT(*) FROM tb_outbox WHERE event_id = ?", event.EventID).Scan(&remaining).Error != nil || remaining != 0 {
				t.Error("outbox cleanup left owned event")
			}
		})
		return event
	}
	enqueue := func(t *testing.T, event Event) {
		t.Helper()
		if err := resources.ORM.Transaction(func(tx *gorm.DB) error { return Enqueue(ctx, tx, event) }); err != nil {
			t.Fatal("cannot enqueue independent outbox fixture")
		}
	}

	t.Run("enqueue_uses_transaction_and_checks_duplicate_semantics", func(t *testing.T) {
		event := fixtureEvent(t, "ACTIVITY_OPEN")
		tx := resources.ORM.Begin()
		if tx.Error != nil || Enqueue(ctx, tx, event) != nil || tx.Rollback().Error != nil {
			t.Fatal("cannot exercise transaction-bound enqueue")
		}
		var count int64
		if resources.ORM.Raw("SELECT COUNT(*) FROM tb_outbox WHERE event_id = ?", event.EventID).Scan(&count).Error != nil || count != 0 {
			t.Fatal("rolled-back business transaction left an event")
		}
		enqueue(t, event)
		equivalent := event
		equivalent.Payload = json.RawMessage(`{"version":1,"id":"9007199254740993"}`)
		if Enqueue(ctx, resources.ORM, equivalent) != nil {
			t.Fatal("same semantic event must be idempotent")
		}
		different := event
		different.Payload = json.RawMessage(`{"version":1,"id":"9007199254740992"}`)
		if !errors.Is(Enqueue(ctx, resources.ORM, different), ErrConflict) {
			t.Fatal("same event ID with different payload must fail")
		}
		different = event
		different.EventID += "_different"
		if !errors.Is(Enqueue(ctx, resources.ORM, different), ErrConflict) {
			t.Fatal("same transition cannot silently replace its event identity")
		}
		if resources.ORM.Exec("UPDATE tb_outbox SET delivery_status = 'DONE', completed_at = NOW(3) WHERE event_id = ?", event.EventID).Error != nil || Enqueue(ctx, resources.ORM, equivalent) != nil {
			t.Fatal("completed enqueue must remain idempotent")
		}
		var status string
		if resources.ORM.Raw("SELECT delivery_status FROM tb_outbox WHERE event_id = ?", event.EventID).Scan(&status).Error != nil || status != "DONE" {
			t.Fatal("duplicate enqueue revived a completed event")
		}
	})

	t.Run("claim_does_not_touch_orders_community_or_unknown_events", func(t *testing.T) {
		for _, action := range []string{"ACTIVITY_PREPARE", "ACTIVITY_OPEN", "ACTIVITY_CLOSE"} {
			enqueue(t, fixtureEvent(t, action))
		}
		ignored := []Event{fixtureEvent(t, "ORDER_CHANGED"), fixtureEvent(t, "ACTIVITY_PREPARE"), fixtureEvent(t, "COMMUNITY_NOTE"), fixtureEvent(t, "ACTIVITY_UNKNOWN"), fixtureEvent(t, "ACTIVITY_OPEN"), fixtureEvent(t, "activity_open")}
		ignored[0].AggregateType = "ORDER"
		ignored[1].AggregateType = "ORDER"
		ignored[2].AggregateType = "COMMUNITY"
		ignored[4].AggregateType = "activity"
		for _, event := range ignored {
			enqueue(t, event)
			if resources.ORM.Exec("UPDATE tb_outbox SET delivery_status = 'PROCESSING', attempts = 7, lease_until = TIMESTAMPADD(SECOND,-1,NOW(3)) WHERE event_id = ?", event.EventID).Error != nil {
				t.Fatal("cannot arrange expired non-activity lease")
			}
		}
		for index := 0; index < 3; index++ {
			event, err := repository.ClaimActivity(ctx)
			if err != nil || event == nil || event.AggregateType != "ACTIVITY" || event.Attempts != 1 {
				t.Fatal("claim must select only supported activity events")
			}
			if repository.Done(ctx, *event) != nil {
				t.Fatal("cannot complete supported activity event")
			}
		}
		if event, err := repository.ClaimActivity(ctx); err != nil || event != nil {
			t.Fatal("unsupported expired leases were claimed")
		}
		for _, event := range ignored {
			var row struct {
				DeliveryStatus string
				Attempts       int
			}
			if resources.ORM.Raw("SELECT delivery_status, attempts FROM tb_outbox WHERE event_id = ?", event.EventID).Scan(&row).Error != nil || row.DeliveryStatus != "PROCESSING" || row.Attempts != 7 {
				t.Fatal("worker changed an order, community, or unsupported event")
			}
		}
	})

	t.Run("expired_and_reclaimed_leases_fence_stale_completion_and_retry", func(t *testing.T) {
		event := fixtureEvent(t, "ACTIVITY_OPEN")
		enqueue(t, event)
		first, err := repository.ClaimActivity(ctx)
		if err != nil || first == nil || first.EventID != event.EventID {
			t.Fatal("cannot claim first lease")
		}
		if resources.ORM.Exec("UPDATE tb_outbox SET lease_until = TIMESTAMPADD(SECOND,-1,NOW(3)) WHERE event_id = ?", event.EventID).Error != nil {
			t.Fatal("cannot expire exact owned lease")
		}
		if !errors.Is(repository.Done(ctx, *first), ErrLeaseLost) {
			t.Fatal("expired worker must not complete even before another claim")
		}
		second, err := repository.ClaimActivity(ctx)
		if err != nil || second == nil || second.ID != first.ID || second.Attempts != first.Attempts+1 {
			t.Fatal("reclaim must advance the lease generation")
		}
		if !errors.Is(repository.Done(ctx, *first), ErrLeaseLost) || !errors.Is(repository.Retry(ctx, *first, errors.New("stale failure")), ErrLeaseLost) {
			t.Fatal("old worker overwrote a newer claim")
		}
		if repository.Done(ctx, *second) != nil || !errors.Is(repository.Retry(ctx, *first, errors.New("late failure")), ErrLeaseLost) {
			t.Fatal("old worker reverted the durable newer DONE result")
		}
	})

	t.Run("concurrent_claims_are_distinct_and_skip_locked_rows", func(t *testing.T) {
		first := fixtureEvent(t, "ACTIVITY_OPEN")
		second := fixtureEvent(t, "ACTIVITY_CLOSE")
		enqueue(t, first)
		enqueue(t, second)
		tx := resources.ORM.Begin()
		defer tx.Rollback()
		var id uint64
		if tx.Raw("SELECT id FROM tb_outbox WHERE event_id = ? FOR UPDATE", first.EventID).Scan(&id).Error != nil {
			t.Fatal("cannot hold exact first row")
		}
		skip, err := repository.ClaimActivity(ctx)
		if err != nil || skip == nil || skip.EventID != second.EventID || repository.Done(ctx, *skip) != nil {
			t.Fatal("claim did not skip another transaction's locked row")
		}
		if tx.Rollback().Error != nil {
			t.Fatal("cannot release exact row lock")
		}
		third := fixtureEvent(t, "ACTIVITY_PREPARE")
		enqueue(t, third)
		var wait sync.WaitGroup
		claims := make(chan *Event, 2)
		failures := make(chan error, 2)
		for index := 0; index < 2; index++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				event, err := repository.ClaimActivity(ctx)
				claims <- event
				failures <- err
			}()
		}
		wait.Wait()
		close(claims)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal("concurrent activity claim failed")
			}
		}
		seen := map[uint64]bool{}
		for event := range claims {
			if event == nil || seen[event.ID] || event.Attempts != 1 {
				t.Fatal("two workers claimed the same current delivery lease")
			}
			seen[event.ID] = true
			if repository.Done(ctx, *event) != nil {
				t.Fatal("cannot complete independently claimed delivery")
			}
		}
	})

	t.Run("continued_failures_keep_pending_and_bound_error_and_backoff", func(t *testing.T) {
		event := fixtureEvent(t, "ACTIVITY_OPEN")
		enqueue(t, event)
		for attempt := 1; attempt <= 10; attempt++ {
			if resources.ORM.Exec("UPDATE tb_outbox SET next_attempt_at = NOW(3) WHERE event_id = ?", event.EventID).Error != nil {
				t.Fatal("cannot advance owned retry")
			}
			claimed, err := repository.ClaimActivity(ctx)
			if err != nil || claimed == nil || claimed.Attempts != attempt {
				t.Fatal("retry did not obtain a new fenced lease")
			}
			if repository.Retry(ctx, *claimed, errors.New(strings.Repeat("故", 600))) != nil {
				t.Fatal("durable retry failed")
			}
			var row struct {
				DeliveryStatus string
				Attempts       int
				LastError      string
				Delay          int
			}
			if resources.ORM.Raw(`SELECT delivery_status, attempts, last_error, TIMESTAMPDIFF(SECOND,NOW(3),next_attempt_at) AS delay
				FROM tb_outbox WHERE event_id = ?`, event.EventID).Scan(&row).Error != nil || row.DeliveryStatus != "PENDING" || row.Attempts != attempt || len([]rune(row.LastError)) != 500 || row.Delay < 0 || row.Delay > 60 {
				t.Fatal("outage retry was dropped, unbounded, or not durable")
			}
		}
	})
}
