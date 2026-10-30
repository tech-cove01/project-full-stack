package communitycards

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
)

func waitObservation(t *testing.T, recorder *taildiag.Recorder, check func(taildiag.Snapshot) bool) taildiag.Snapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		snapshot := recorder.Snapshot()
		if check(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatal("controlled card observation did not enter the expected stage")
		}
		runtime.Gosched()
	}
}

func TestObservationsSeparateSharedSQLAndSlotQueueAfterCallerCancellation(t *testing.T) {
	store, cfg := fakeStore(), defaults()
	cfg.active = 1
	first, second := Ref{1, 1}, Ref{2, 1}
	store.cards[first], store.cards[second] = Card{Title: "first"}, Card{Title: "second"}
	entered, release := make(chan time.Duration, 2), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	type privateKey struct{}
	store.loadHook = func(ctx context.Context, _ []Ref) error {
		if ctx.Value(privateKey{}) != nil {
			return errors.New("shared loader inherited caller data")
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("shared loader lost its deadline")
		}
		entered <- time.Until(deadline)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	recorder := taildiag.New()
	cache := testCache(t, store, cfg).WithDiagnostics(recorder)
	firstCtx, cancelFirst := context.WithCancel(context.WithValue(context.Background(), privateKey{}, "caller-only"))
	defer cancelFirst()
	firstCtx, finishFirst := taildiag.Start(firstCtx, recorder, false)
	secondCtx, finishSecond := taildiag.Start(context.Background(), recorder, false)
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := cache.GetMany(firstCtx, []Ref{first}); firstDone <- err }()
	if remaining := await(t, entered); remaining < time.Second || remaining > cfg.loadTimeout {
		t.Fatalf("shared SQL did not retain its independent application deadline: %s", remaining)
	}
	go func() { _, err := cache.GetMany(secondCtx, []Ref{second}); secondDone <- err }()
	waitObservation(t, recorder, func(snapshot taildiag.Snapshot) bool {
		return snapshot.Stages[taildiag.CardsMany].Active == 2 &&
			snapshot.Stages[taildiag.CardsShared].Active == 2 &&
			snapshot.Stages[taildiag.CardsColdSQL].Active == 1 &&
			snapshot.Stages[taildiag.CardsSlotWait].Active == 1
	})
	cancelFirst()
	if err := await(t, firstDone); !errors.Is(err, context.Canceled) {
		t.Fatal("first caller did not cancel independently")
	}
	finishFirst(true)
	waitObservation(t, recorder, func(snapshot taildiag.Snapshot) bool {
		return snapshot.Stages[taildiag.CardsMany].Active == 1 &&
			snapshot.Stages[taildiag.CardsShared].Active == 2 &&
			snapshot.Stages[taildiag.CardsColdSQL].Active == 1 &&
			snapshot.Stages[taildiag.CardsSlotWait].Active == 1
	})
	// Once the real waiting boundaries were observed, release both synthetic
	// content loads. Cancellation above must not have ended shared SQL or queue.
	releaseOnce.Do(func() { close(release) })
	if err := await(t, secondDone); err != nil {
		t.Fatal("second caller inherited first caller cancellation")
	}
	finishSecond(false)
	if cache.Close() != nil {
		t.Fatal("cache close failed")
	}
	snapshot := recorder.Snapshot()
	for _, stage := range []taildiag.Stage{taildiag.CardsMany, taildiag.CardsShared, taildiag.CardsSlotWait, taildiag.CardsRecheck, taildiag.CardsColdSQL, taildiag.CardsFill} {
		if aggregate := snapshot.Stages[stage]; aggregate.Count != 2 || aggregate.Active != 0 {
			t.Fatalf("card stage lost work or remained active: %+v", aggregate)
		}
	}
	if snapshot.Stages[taildiag.CardsMany].Errors != 1 || snapshot.Stages[taildiag.CardsShared].Errors != 0 {
		t.Fatal("caller cancellation changed the shared outcome")
	}
}
