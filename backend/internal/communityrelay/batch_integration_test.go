package communityrelay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Global ClaimBatch fixtures require a dedicated empty integration database.
// They own only their exact event IDs and never truncate/reset a shared table.
type batchFixture struct {
	resources *database.Resources
	ctx       context.Context
	prefix    string
	ids       []string
}

func newBatchFixture(t *testing.T) *batchFixture {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	if !strings.HasPrefix(cfg.MySQL.Database, "cap10_relay_it_") {
		t.Skip("registered independent CAP10 database required for global batch fixtures")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	resources.ORM = resources.ORM.WithContext(ctx)
	t.Cleanup(func() { _ = resources.Close() })
	var count int64
	if err = resources.ORM.WithContext(ctx).Raw("SELECT COUNT(*) FROM tb_community_outbox").Scan(&count).Error; err != nil || count != 0 {
		t.Fatal("batch regression requires an empty isolated outbox; existing rows remain untouched")
	}
	fixture := &batchFixture{resources: resources, ctx: ctx, prefix: "RELAY-BATCH:" + randomSuffix(t)}
	t.Cleanup(func() {
		if len(fixture.ids) == 0 {
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := resources.ORM.WithContext(cleanup).Exec("DELETE FROM tb_community_outbox WHERE event_id IN ?", fixture.ids).Error; err != nil {
			t.Error("exact owned batch fixture cleanup failed")
		}
	})
	return fixture
}

func (fixture *batchFixture) seed(t *testing.T, count int) []Event {
	t.Helper()
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s:%d", fixture.prefix, len(fixture.ids))
		fixture.ids = append(fixture.ids, ids[i])
		if err := fixture.resources.ORM.WithContext(fixture.ctx).Exec(`INSERT INTO tb_community_outbox
			(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload)
			VALUES (?,'FOLLOW','42',1,'FOLLOW_CHANGED','{}')`, ids[i]).Error; err != nil {
			t.Fatal("owned batch fixture insert failed")
		}
	}
	var events []Event
	if err := fixture.resources.ORM.WithContext(fixture.ctx).Raw("SELECT id,event_id,attempts FROM tb_community_outbox WHERE event_id IN ? ORDER BY id", ids).Scan(&events).Error; err != nil || len(events) != count {
		t.Fatal("owned batch fixture lookup failed")
	}
	return events
}

type batchState struct {
	ID                uint64
	Status            string
	Attempts          int
	Leased, Completed bool
}

func (fixture *batchFixture) states(t *testing.T) []batchState {
	t.Helper()
	var states []batchState
	if err := fixture.resources.ORM.WithContext(fixture.ctx).Raw(`SELECT id,delivery_status AS status,attempts,
		lease_until IS NOT NULL AS leased,completed_at IS NOT NULL AS completed
		FROM tb_community_outbox WHERE event_id IN ? ORDER BY id`, fixture.ids).Scan(&states).Error; err != nil {
		t.Fatal(err)
	}
	return states
}

func TestIntegrationClaimBatchEligibilityExhaustedHeadAndSkipLocked(t *testing.T) {
	f := newBatchFixture(t)
	events := f.seed(t, 5)
	db, repo := f.resources.ORM, NewRepository(f.resources.ORM)
	if err := db.Exec("UPDATE tb_community_outbox SET delivery_status='PROCESSING',attempts=8,lease_until=DATE_SUB(NOW(3),INTERVAL 1 SECOND) WHERE id=?", events[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE tb_community_outbox SET next_attempt_at=DATE_ADD(NOW(3),INTERVAL 1 HOUR) WHERE id=?", events[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE tb_community_outbox SET attempts=7 WHERE id=?", events[4].ID).Error; err != nil {
		t.Fatal(err)
	}
	locked := db.WithContext(f.ctx).Begin()
	if locked.Error != nil {
		t.Fatal(locked.Error)
	}
	defer locked.Rollback()
	var id uint64
	if err := locked.Raw("SELECT id FROM tb_community_outbox WHERE id=? FOR UPDATE", events[2].ID).Scan(&id).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimBatch(f.ctx, MaxBatchSize)
	if err != nil || len(claimed) != 2 || claimed[0].ID != events[3].ID || claimed[1].ID != events[4].ID || claimed[0].Attempts != 1 || claimed[1].Attempts != 8 {
		t.Fatalf("bounded claim must park exhausted head, skip future/locked rows and retain fences: %v", err)
	}
	states := f.states(t)
	if states[0].Status != "NEEDS_REVIEW" || states[0].Attempts != 8 || states[0].Leased {
		t.Fatal("exhausted head must park without attempt nine")
	}
	if err := repo.DoneBatch(f.ctx, claimed); err != nil {
		t.Fatal(err)
	}
	if err := locked.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.ClaimBatch(f.ctx, 2)
	if err != nil || len(claimed) != 1 || claimed[0].ID != events[2].ID {
		t.Fatal("released row must remain available without revisiting parked history")
	}
	if err := repo.DoneBatch(f.ctx, claimed); err != nil {
		t.Fatal(err)
	}
}

type batchCancelTrace struct {
	logger.Interface
	cancel context.CancelFunc
	once   sync.Once
}

func (trace *batchCancelTrace) Trace(_ context.Context, _ time.Time, query func() (string, int64), err error) {
	text, _ := query()
	if err == nil && strings.Contains(strings.ReplaceAll(text, "o.", ""), "attempts=attempts+1") {
		trace.once.Do(trace.cancel)
	}
}

func TestIntegrationClaimBatchCancellationRollsBackParkAndLeasedPrefix(t *testing.T) {
	f := newBatchFixture(t)
	events := f.seed(t, 3)
	if err := f.resources.ORM.Exec("UPDATE tb_community_outbox SET delivery_status='PROCESSING',attempts=8,lease_until=DATE_SUB(NOW(3),INTERVAL 1 SECOND) WHERE id=?", events[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	before := f.states(t)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	trace := &batchCancelTrace{Interface: logger.Default.LogMode(logger.Silent), cancel: cancel}
	repo := NewRepository(f.resources.ORM.Session(&gorm.Session{Logger: trace}))
	claimed, err := repo.ClaimBatch(ctx, 3)
	if err == nil || claimed != nil {
		t.Fatal("cancel after an actual lease update must not expose a publishable prefix")
	}
	if !reflect.DeepEqual(before, f.states(t)) {
		t.Fatal("canceled claim must roll back both earlier parking and lease/attempt changes")
	}
}

func TestIntegrationDoneBatchStaleMiddleRollsBackBeforeIndividualFallback(t *testing.T) {
	for _, stolen := range []bool{false, true} {
		t.Run(fmt.Sprint("reclaimed_", stolen), func(t *testing.T) {
			f := newBatchFixture(t)
			f.seed(t, 3)
			repo := NewRepository(f.resources.ORM)
			claimed, err := repo.ClaimBatch(f.ctx, 3)
			if err != nil || len(claimed) != 3 {
				t.Fatal("batch claim failed")
			}
			if err := f.resources.ORM.Exec("UPDATE tb_community_outbox SET lease_until=DATE_SUB(NOW(3),INTERVAL 1 SECOND) WHERE id=?", claimed[1].ID).Error; err != nil {
				t.Fatal(err)
			}
			var replacement []Event
			if stolen {
				replacement, err = repo.ClaimBatch(f.ctx, 1)
				if err != nil || len(replacement) != 1 || replacement[0].ID != claimed[1].ID || replacement[0].Attempts != 2 {
					t.Fatal("expired middle lease must be independently reclaimable")
				}
			}
			before := f.states(t)
			if err := repo.DoneBatch(f.ctx, []Event{claimed[2], claimed[0], claimed[1]}); !errors.Is(err, ErrLeaseLost) {
				t.Fatal("stale middle fence must reject the whole transaction")
			}
			if !reflect.DeepEqual(before, f.states(t)) {
				t.Fatal("failed done transaction committed an earlier prefix or overwrote the new owner")
			}
			for i, event := range claimed {
				err := repo.Done(f.ctx, event)
				if i == 1 && !errors.Is(err, ErrLeaseLost) || i != 1 && err != nil {
					t.Fatal("individual fallback must complete only still-owned events")
				}
			}
			if !stolen {
				replacement, err = repo.ClaimBatch(f.ctx, 1)
			}
			if err != nil || len(replacement) != 1 || repo.DoneBatch(f.ctx, replacement) != nil {
				t.Fatal("the valid later owner must still be able to complete")
			}
			for _, state := range f.states(t) {
				if state.Status != "DONE" || state.Leased || !state.Completed {
					t.Fatal("recovered batch did not fully converge")
				}
			}
		})
	}
}

func TestIntegrationClaimBatchDoesNotLockIneligibleLiveHead(t *testing.T) {
	f := newBatchFixture(t)
	events := f.seed(t, 3)
	db := f.resources.ORM
	if err := db.Exec("UPDATE tb_community_outbox SET delivery_status='PROCESSING',attempts=1,lease_until=DATE_ADD(NOW(3),INTERVAL 1 HOUR) WHERE id=?", events[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	events[0].Attempts = 1
	gate := &claimScanGate{Interface: logger.Default.LogMode(logger.Silent), entered: make(chan struct{}), release: make(chan struct{})}
	defer gate.resume()
	type outcome struct {
		events []Event
		err    error
	}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	result := make(chan outcome, 1)
	go func() {
		rows, err := NewRepository(db.Session(&gorm.Session{Logger: gate})).ClaimBatch(ctx, 2)
		result <- outcome{rows, err}
	}()
	defer func() { gate.resume(); cancel() }()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual batch SELECT did not enter the trace gate")
	}
	done := make(chan error, 1)
	go func() { done <- NewRepository(db).Done(ctx, events[0]) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		gate.resume()
		t.Fatal("RC batch selection blocked a nonmatching live lease's original owner")
	}
	gate.resume()
	claimed := <-result
	if claimed.err != nil || len(claimed.events) != 2 || claimed.events[0].ID != events[1].ID {
		t.Fatal("due tail leases must remain available in ID order")
	}
	if err := NewRepository(db).DoneBatch(f.ctx, claimed.events); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationEightConcurrentClaimBatchesAreUniqueAndConverge(t *testing.T) {
	f := newBatchFixture(t)
	f.seed(t, 256)
	repo := NewRepository(f.resources.ORM)
	var workers sync.WaitGroup
	var guard sync.Mutex
	seen := make(map[uint64]bool)
	failures := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				events, err := repo.ClaimBatch(f.ctx, 8)
				if err != nil {
					failures <- err
					return
				}
				if len(events) == 0 {
					return
				}
				guard.Lock()
				for _, event := range events {
					if seen[event.ID] || event.Attempts != 1 {
						guard.Unlock()
						failures <- errors.New("duplicate batch claim or changed attempt")
						return
					}
					seen[event.ID] = true
				}
				guard.Unlock()
				if err := repo.DoneBatch(f.ctx, events); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if len(seen) != 256 {
		t.Fatal("eight lanes did not claim all owned events exactly once")
	}
	for _, state := range f.states(t) {
		if state.Status != "DONE" || state.Attempts != 1 || state.Leased || !state.Completed {
			t.Fatal("bounded concurrent batches did not converge with their original fences")
		}
	}
}
