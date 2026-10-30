package taildiag

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOldestActiveMovesWhenOldestOperationEnds(t *testing.T) {
	var live activeStage
	first, second := live.begin(10), live.begin(20)
	active, tracked, untracked, oldest, _ := live.snapshot(100)
	if active != 2 || tracked != 2 || untracked != 0 || oldest != 90 {
		t.Fatal("in-flight ages missing before completion")
	}
	live.end(first)
	active, tracked, untracked, oldest, _ = live.snapshot(100)
	if active != 1 || tracked != 1 || untracked != 0 || oldest != 80 {
		t.Fatal("completed oldest left stale age")
	}
	live.end(second)
	active, tracked, untracked, oldest, _ = live.snapshot(100)
	if active != 0 || tracked != 0 || untracked != 0 || oldest != 0 {
		t.Fatal("completed stage retained active state")
	}
}

func TestActiveClockBudgetMarksMissingAgesInsteadOfZero(t *testing.T) {
	recorder := New()
	ctx, finish := Start(context.Background(), recorder, false)
	defer finish(false)
	spans := make([]*Span, MaxActivePerStage+1)
	for i := range spans {
		spans[i] = Begin(ctx, CardsSlotWait, 0)
	}
	stage := recorder.Snapshot().Stages[CardsSlotWait]
	if stage.Active != MaxActivePerStage+1 || stage.TrackedActive != MaxActivePerStage || stage.UntrackedActive != 1 || stage.OldestComplete || stage.ActiveTrackingOverflows != 1 {
		t.Fatalf("bounded age registry hid overflow: %+v", stage)
	}
	for _, span := range spans {
		span.End(false)
		span.End(true)
	}
	stage = recorder.Snapshot().Stages[CardsSlotWait]
	if stage.Active != 0 || stage.TrackedActive != 0 || stage.UntrackedActive != 0 || !stage.OldestComplete || stage.OldestActiveNS != 0 || stage.Count != MaxActivePerStage+1 || stage.Errors != 0 {
		t.Fatal("ending overflowed operations did not converge exactly once")
	}
}

func TestSharedObservationHasIndependentScopeAndLifetime(t *testing.T) {
	recorder := New()
	caller, cancel := context.WithCancel(context.Background())
	caller, finishCaller := Start(caller, recorder, true)
	application, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	shared, finishShared := StartShared(application, recorder)
	if fromContext(caller) == fromContext(shared) || fromContext(shared).scope != "cards.shared" {
		t.Fatal("shared observation reused HTTP trace")
	}
	cancel()
	if shared.Err() != nil {
		t.Fatal("caller canceled application-owned observation")
	}
	fromContext(caller).started = time.Now().Add(-SlowThreshold - time.Millisecond)
	fromContext(shared).started = time.Now().Add(-SlowThreshold - time.Millisecond)
	span := Begin(shared, CardsColdSQL, 20)
	if recorder.Snapshot().Stages[CardsColdSQL].Active != 1 {
		t.Fatal("independent shared stage invisible before finish")
	}
	span.End(false)
	finishShared(false)
	finishCaller(true)
	out := recorder.Snapshot()
	if out.Requests != 1 || out.SharedTasks != 1 {
		t.Fatal("shared completions inflated request count")
	}
	if len(out.SlowTraces) != 2 || out.SlowTraces[0].Scope != "cards.shared" || out.SlowTraces[0].Canceled || !out.SlowTraces[1].Canceled {
		t.Fatal("shared/request scopes or cancellation mixed")
	}
	encoded, err := json.Marshal(out)
	if err != nil || strings.Contains(string(encoded), "context.Background") {
		t.Fatal("observer exported arbitrary context")
	}
}

func TestZeroRecorderCanObserveBeforeNew(t *testing.T) {
	var recorder Recorder
	if !recorder.Snapshot().Enabled {
		t.Fatal("zero recorder disabled")
	}
	ctx, finish := StartShared(context.Background(), &recorder)
	span := Begin(ctx, CardsFill, 0)
	span.End(false)
	finish(false)
	if recorder.Snapshot().Stages[CardsFill].Count != 1 {
		t.Fatal("zero recorder lost observations")
	}
}
