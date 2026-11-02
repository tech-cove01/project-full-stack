package communityrelay

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func boundedFixtureEvents(t *testing.T, fixture *batchFixture) []Event {
	t.Helper()
	var events []Event
	if err := fixture.resources.ORM.WithContext(fixture.ctx).Raw(`SELECT id,event_id,aggregate_type,aggregate_id,
		aggregate_version,event_type,payload,attempts FROM tb_community_outbox WHERE event_id IN ? ORDER BY id`, fixture.ids).Scan(&events).Error; err != nil || len(events) != len(fixture.ids) {
		t.Fatal("owned byte-prefix fixture lookup failed")
	}
	return events
}

func TestIntegrationClaimBatchBoundedPrefixAndOversizedSingletonLeaveTailUntouched(t *testing.T) {
	f := newBatchFixture(t)
	seeded := f.seed(t, 3)
	payload, _ := json.Marshal(map[string]string{"text": strings.Repeat("x", 4000)})
	if err := f.resources.ORM.Exec("UPDATE tb_community_outbox SET payload=? WHERE id=?", string(payload), seeded[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	rows := boundedFixtureEvents(t, f)
	before := f.states(t)
	budget := int(eventEncodedUpperBound(rows[0]) + eventEncodedUpperBound(rows[2]))
	repo := NewRepository(f.resources.ORM)
	claimed, err := repo.ClaimBatchBounded(f.ctx, 3, budget)
	if err != nil || len(claimed) != 1 || claimed[0].ID != rows[0].ID {
		t.Fatal("large middle row must end the byte prefix rather than be skipped")
	}
	states := f.states(t)
	if !reflect.DeepEqual(before[1:], states[1:]) || states[0].Attempts != 1 || !states[0].Leased {
		t.Fatal("unselected rows were leased, changed status or charged attempts")
	}
	if err := repo.DoneBatch(f.ctx, claimed); err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.ClaimBatchBounded(f.ctx, 3, budget)
	if err != nil || len(claimed) != 1 || claimed[0].ID != rows[1].ID || eventEncodedUpperBound(claimed[0]) <= uint64(budget) {
		t.Fatal("a first oversized event must advance as an explicitly over-budget singleton")
	}
	if f.states(t)[2] != before[2] {
		t.Fatal("oversized singleton consumed or charged a later row")
	}
	if err := repo.DoneBatch(f.ctx, claimed); err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.ClaimBatchBounded(f.ctx, 3, budget)
	if err != nil || len(claimed) != 1 || claimed[0].ID != rows[2].ID || repo.DoneBatch(f.ctx, claimed) != nil {
		t.Fatal("untouched tail could not later be claimed and completed")
	}
}

func TestIntegrationClaimBatchBoundedExactByteBoundary(t *testing.T) {
	for _, test := range []struct {
		name       string
		adjustment int
	}{{"exact_fit", 0}, {"one_byte_short", -1}} {
		t.Run(test.name, func(t *testing.T) {
			f := newBatchFixture(t)
			f.seed(t, 2)
			rows := boundedFixtureEvents(t, f)
			budget := int(eventEncodedUpperBound(rows[0])+eventEncodedUpperBound(rows[1])) + test.adjustment
			claimed, err := NewRepository(f.resources.ORM).ClaimBatchBounded(f.ctx, 2, budget)
			want := 2
			if test.adjustment < 0 {
				want = 1
			}
			if err != nil || len(claimed) != want {
				t.Fatal("exact byte boundary changed the leased prefix")
			}
			states := f.states(t)
			if want == 1 && (states[1].Status != "PENDING" || states[1].Attempts != 0 || states[1].Leased) {
				t.Fatal("one-byte-short budget mutated an unselected row")
			}
		})
	}
}

func TestIntegrationClaimBatchBoundedExhaustedHeadDoesNotConsumeBudget(t *testing.T) {
	f := newBatchFixture(t)
	seeded := f.seed(t, 3)
	if err := f.resources.ORM.Exec(`UPDATE tb_community_outbox SET delivery_status='PROCESSING',attempts=8,
		lease_until=DATE_SUB(NOW(3),INTERVAL 1 SECOND) WHERE id=?`, seeded[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	rows := boundedFixtureEvents(t, f)
	claimed, err := NewRepository(f.resources.ORM).ClaimBatchBounded(f.ctx, 3, int(eventEncodedUpperBound(rows[1])))
	if err != nil || len(claimed) != 1 || claimed[0].ID != rows[1].ID {
		t.Fatal("parking exhausted head swallowed the next fitting event")
	}
	states := f.states(t)
	if states[0].Status != "NEEDS_REVIEW" || states[0].Attempts != 8 || states[0].Leased ||
		states[2].Status != "PENDING" || states[2].Attempts != 0 || states[2].Leased {
		t.Fatal("exhausted or unselected tail state differs from the original contract")
	}
}

func TestIntegrationClaimBatchBoundedCancellationRollsBackWholePrefix(t *testing.T) {
	f := newBatchFixture(t)
	f.seed(t, 3)
	rows := boundedFixtureEvents(t, f)
	before := f.states(t)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	trace := &batchCancelTrace{Interface: logger.Default.LogMode(logger.Silent), cancel: cancel}
	repo := NewRepository(f.resources.ORM.Session(&gorm.Session{Logger: trace}))
	budget := int(eventEncodedUpperBound(rows[0]) + eventEncodedUpperBound(rows[1]))
	claimed, err := repo.ClaimBatchBounded(ctx, 3, budget)
	if err == nil || claimed != nil || !reflect.DeepEqual(before, f.states(t)) {
		t.Fatal("cancellation after a real prefix update must roll back every lease and attempt")
	}
}
