package communityrelay

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

type runnerTrace struct {
	mu    sync.Mutex
	calls []string
}

func (trace *runnerTrace) add(call string) {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.calls = append(trace.calls, call)
}

func (trace *runnerTrace) snapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.calls...)
}

type runnerRepositoryFixture struct {
	trace       *runnerTrace
	events      []Event
	claim       func(context.Context, int, int) ([]Event, error)
	doneBatch   func(context.Context, []Event) error
	done        func(context.Context, Event) error
	retry       func(context.Context, Event, error) error
	needsReview func(context.Context, Event, error) error
}

func (repo *runnerRepositoryFixture) ClaimBatchBounded(ctx context.Context, count, budget int) ([]Event, error) {
	repo.trace.add("claim")
	if repo.claim != nil {
		return repo.claim(ctx, count, budget)
	}
	return append([]Event(nil), repo.events...), nil
}
func (repo *runnerRepositoryFixture) DoneBatch(ctx context.Context, events []Event) error {
	repo.trace.add("done-batch")
	if repo.doneBatch != nil {
		return repo.doneBatch(ctx, events)
	}
	return nil
}
func (repo *runnerRepositoryFixture) Done(ctx context.Context, event Event) error {
	repo.trace.add(fmt.Sprintf("done-%d", event.ID))
	if repo.done != nil {
		return repo.done(ctx, event)
	}
	return nil
}
func (repo *runnerRepositoryFixture) Retry(ctx context.Context, event Event, cause error) error {
	repo.trace.add(fmt.Sprintf("retry-%d", event.ID))
	if repo.retry != nil {
		return repo.retry(ctx, event, cause)
	}
	return nil
}
func (repo *runnerRepositoryFixture) NeedsReview(ctx context.Context, event Event, cause error) error {
	repo.trace.add(fmt.Sprintf("review-%d", event.ID))
	if repo.needsReview != nil {
		return repo.needsReview(ctx, event, cause)
	}
	return nil
}

type runnerPublisherFixture struct {
	trace   *runnerTrace
	publish func([]Event) error
	close   func() error
}

func (publisher *runnerPublisherFixture) Publish(events []Event) error {
	publisher.trace.add("publish")
	if publisher.publish != nil {
		return publisher.publish(events)
	}
	return nil
}
func (publisher *runnerPublisherFixture) Close() error {
	publisher.trace.add("close")
	if publisher.close != nil {
		return publisher.close()
	}
	return nil
}

func newRunnerFixture(t *testing.T, ctx context.Context, events []Event) (*BatchRunner, *runnerRepositoryFixture, *runnerPublisherFixture, *runnerTrace) {
	t.Helper()
	trace := new(runnerTrace)
	repo := &runnerRepositoryFixture{trace: trace, events: events}
	publisher := &runnerPublisherFixture{trace: trace}
	runner := &BatchRunner{lifetime: ctx, repo: repo, publisher: publisher, batchSize: 32}
	t.Cleanup(func() { _ = runner.Close() })
	return runner, repo, publisher, trace
}

func TestBatchRunnerConstructorAndAcknowledgementOrdering(t *testing.T) {
	if runner, err := NewBatchRunner(context.Background(), nil, []string{"broker"}, "topic", 32); runner != nil || !errors.Is(err, ErrInvalidRunner) {
		t.Fatal("nil repository configuration was accepted")
	}
	if runner, err := NewBatchRunner(context.Background(), &gorm.DB{}, []string{"broker"}, "topic", 65); runner != nil || !errors.Is(err, ErrInvalidPublisher) {
		t.Fatal("invalid bounded publisher configuration was accepted")
	}
	events := publisherEvents(3)
	runner, repo, publisher, trace := newRunnerFixture(t, context.Background(), events)
	repo.claim = func(ctx context.Context, count, budget int) ([]Event, error) {
		if ctx != runner.lifetime || count != 32 || budget != BatchPublishBytes {
			t.Fatal("claim did not use the same lifetime and conservative byte/count policy")
		}
		return events, nil
	}
	publisher.publish = func(got []Event) error {
		if !reflect.DeepEqual(got, events) || !reflect.DeepEqual(trace.snapshot(), []string{"claim", "publish"}) {
			t.Fatal("events changed order or SQL completion preceded the acknowledgement")
		}
		return nil
	}
	if processed, err := runner.Poll(); !processed || err != nil || !reflect.DeepEqual(trace.snapshot(), []string{"claim", "publish", "done-batch"}) {
		t.Fatal("synchronously acknowledged batch was not completed exactly once")
	}
}

func TestBatchRunnerPoisonParkingFailurePreservesOtherAcknowledgements(t *testing.T) {
	events := publisherEvents(3)
	events[1].Payload = []byte("invalid json")
	runner, repo, publisher, trace := newRunnerFixture(t, context.Background(), events)
	parkError := errors.New("park fence lost")
	repo.needsReview = func(_ context.Context, event Event, cause error) error {
		if event.ID != events[1].ID || cause == nil {
			t.Fatal("poison event identity or failure cause was discarded")
		}
		return parkError
	}
	publisher.publish = func(valid []Event) error {
		if !reflect.DeepEqual(valid, []Event{events[0], events[2]}) {
			t.Fatal("poison filtering changed the surviving ordered prefix")
		}
		return nil
	}
	if processed, err := runner.Poll(); !processed || !errors.Is(err, parkError) ||
		!reflect.DeepEqual(trace.snapshot(), []string{"claim", "review-2", "publish", "done-batch"}) {
		t.Fatal("failed poison parking was misreported or other valid events could not complete")
	}
}

func TestBatchRunnerWholePublishFailureRetainsBackoffLimitAndFences(t *testing.T) {
	events := publisherEvents(3)
	events[0].Attempts, events[1].Attempts, events[2].Attempts = 1, 8, 2
	runner, repo, publisher, trace := newRunnerFixture(t, context.Background(), events)
	writeError, retryError := errors.New("uncertain publish"), errors.New("retry fence lost")
	publisher.publish = func([]Event) error { return writeError }
	repo.retry = func(_ context.Context, event Event, cause error) error {
		if !errors.Is(cause, writeError) || event.Attempts != events[event.ID-1].Attempts {
			t.Fatal("retry lost its original attempt fence or delivery cause")
		}
		if event.ID == 1 {
			return retryError
		}
		return nil
	}
	if processed, err := runner.Poll(); !processed || !errors.Is(err, writeError) || !errors.Is(err, retryError) ||
		!reflect.DeepEqual(trace.snapshot(), []string{"claim", "publish", "retry-1", "review-2", "retry-3"}) {
		t.Fatal("uncertain publish inferred a partial ACK or weakened retry/review fencing")
	}
}

func TestBatchRunnerOnlyKnownLeaseRollbackAllowsIndividualSalvage(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			runner, repo, _, trace := newRunnerFixture(t, context.Background(), publisherEvents(3))
			failure := errors.New("ambiguous commit result")
			if stale {
				failure = ErrLeaseLost
			}
			repo.doneBatch = func(context.Context, []Event) error { return failure }
			if processed, err := runner.Poll(); !processed || !errors.Is(err, failure) {
				t.Fatal("transaction result was not returned unchanged")
			}
			want := []string{"claim", "publish", "done-batch"}
			if stale {
				want = append(want, "done-1", "done-2", "done-3")
			}
			if !reflect.DeepEqual(trace.snapshot(), want) {
				t.Fatal("ambiguous commit was treated as a rollback or known stale leases were not individually fenced")
			}
		})
	}
}

func TestBatchRunnerCancellationStopsClaimsAndDoesNotInventCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner, _, publisher, trace := newRunnerFixture(t, ctx, publisherEvents(2))
	publisher.publish = func([]Event) error { cancel(); return context.Canceled }
	if processed, err := runner.Poll(); !processed || !errors.Is(err, context.Canceled) ||
		!reflect.DeepEqual(trace.snapshot(), []string{"claim", "publish"}) {
		t.Fatal("cancelled uncertain owner wrote a detached retry or completion")
	}
	if processed, err := runner.Poll(); processed || !errors.Is(err, context.Canceled) || len(trace.snapshot()) != 2 {
		t.Fatal("cancelled lifetime claimed more rows")
	}
}

func TestBatchRunnerConcurrentPollRejectedUntilActualSDKDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	transport := &publisherTransport{partitions: 1, produceHook: func(ctx context.Context, _ publisherProduce) (int16, error) {
		close(entered)
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}}
	sdk, _ := newSDKPublisher(t, ctx, 32, transport)
	runner, _, _, trace := newRunnerFixture(t, ctx, publisherEvents(2))
	runner.publisher = sdk
	done, closed := make(chan error, 1), make(chan error, 1)
	go func() { _, err := runner.Poll(); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("actual SDK Produce did not start")
	}
	cancel()
	if processed, err := runner.Poll(); processed || !errors.Is(err, ErrRunnerBusy) || len(trace.snapshot()) != 1 {
		t.Fatal("another claim overlapped the unresolved actual SDK batch")
	}
	go func() { closed <- runner.Close() }()
	select {
	case <-closed:
		t.Fatal("runner Close returned while the SDK batch still owned the lane")
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := waitPublisherResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal("draining an uncertain batch fabricated an ACK")
	}
	if err := waitPublisherResult(t, closed); err != nil || !reflect.DeepEqual(trace.snapshot(), []string{"claim"}) {
		t.Fatal("cancelled closed runner changed SQL outcomes or failed to join")
	}
}

func TestBatchRunnerRunClosesOnceOnIdleCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner, repo, _, trace := newRunnerFixture(t, ctx, nil)
	claimed := make(chan struct{})
	repo.claim = func(context.Context, int, int) ([]Event, error) { close(claimed); return nil, nil }
	done := make(chan error, 1)
	go func() { runner.Run(); done <- nil }()
	select {
	case <-claimed:
	case <-time.After(time.Second):
		t.Fatal("idle runner did not enter its bounded claim")
	}
	cancel()
	if err := waitPublisherResult(t, done); err != nil || runner.Close() != nil ||
		!reflect.DeepEqual(trace.snapshot(), []string{"claim", "close"}) {
		t.Fatal("Run cancellation did not synchronously close exactly once")
	}
	if processed, err := runner.Poll(); processed || !errors.Is(err, ErrRunnerClosed) {
		t.Fatal("closed runner claimed after its owner joined")
	}
}

// Keep this import tied to the actual pinned SDK contract exercised above.
var _ kafka.RoundTripper = (*publisherTransport)(nil)
