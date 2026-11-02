package communityrelay

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
)

var (
	ErrInvalidRunner = errors.New("invalid community batch runner configuration")
	ErrRunnerBusy    = errors.New("community batch runner already has an active operation")
	ErrRunnerClosed  = errors.New("community batch runner is closed")
)

type batchRunnerRepository interface {
	ClaimBatchBounded(context.Context, int, int) ([]Event, error)
	DoneBatch(context.Context, []Event) error
	Done(context.Context, Event) error
	Retry(context.Context, Event, error) error
	NeedsReview(context.Context, Event, error) error
}

type batchRunnerPublisher interface {
	Publish([]Event) error
	Close() error
}

// BatchRunner is the opt-in count/byte-bounded relay loop. It owns its publisher
// and lifetime; the existing single-event Worker is unchanged.
type BatchRunner struct {
	lifetime  context.Context
	repo      batchRunnerRepository
	publisher batchRunnerPublisher
	batchSize int
	topic     string
	lastEvent string
	mu        sync.Mutex
	closed    bool
	closeErr  error
}

func NewBatchRunner(lifetime context.Context, db *gorm.DB, brokers []string, topic string, batchSize int) (*BatchRunner, error) {
	if db == nil {
		return nil, ErrInvalidRunner
	}
	publisher, err := NewBatchPublisher(lifetime, brokers, topic, batchSize)
	if err != nil {
		return nil, err
	}
	return &BatchRunner{lifetime: lifetime, repo: NewRepository(db), publisher: publisher,
		batchSize: batchSize, topic: topic}, nil
}

// Poll completes one bounded claim without allowing another claim to overlap
// its unresolved publish/drain. Each durable outcome retains the original
// unexpired lease and attempt fence.
func (runner *BatchRunner) Poll() (bool, error) {
	if !runner.mu.TryLock() {
		return false, ErrRunnerBusy
	}
	defer runner.mu.Unlock()
	if runner.closed {
		return false, ErrRunnerClosed
	}
	ctx := runner.lifetime
	if err := ctx.Err(); err != nil {
		return false, err
	}
	events, err := runner.repo.ClaimBatchBounded(ctx, runner.batchSize, BatchPublishBytes)
	if err != nil || len(events) == 0 {
		return false, err
	}
	runner.lastEvent = events[0].EventID
	valid := make([]Event, 0, len(events))
	var failures []error
	for _, event := range events {
		if !json.Valid(event.Payload) {
			cause := errors.New("community event payload is not valid JSON")
			if err := runner.repo.NeedsReview(ctx, event, cause); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		valid = append(valid, event)
	}
	if err := ctx.Err(); err != nil {
		return true, errors.Join(append(failures, err)...)
	}
	if len(valid) == 0 {
		return true, errors.Join(failures...)
	}
	if err := runner.publisher.Publish(valid); err != nil {
		// Publish has really drained the uncertain Writer before returning.
		// A cancelled owner leaves leases for replay; it never invents an ACK
		// or starts detached completion SQL after the application cancelled.
		failures = append(failures, err)
		if ctx.Err() != nil {
			return true, errors.Join(append(failures, ctx.Err())...)
		}
		for _, event := range valid {
			var outcome error
			if event.Attempts >= maxAttempts {
				outcome = runner.repo.NeedsReview(ctx, event, err)
			} else {
				outcome = runner.repo.Retry(ctx, event, err)
			}
			if outcome != nil {
				failures = append(failures, outcome)
			}
		}
		return true, errors.Join(failures...)
	}
	if err := runner.repo.DoneBatch(ctx, valid); err != nil {
		failures = append(failures, err)
		// Only the known stale-lease rollback permits individual salvage.
		// Other transaction/commit errors remain uncertain and are returned.
		if errors.Is(err, ErrLeaseLost) {
			for _, event := range valid {
				if err := runner.repo.Done(ctx, event); err != nil {
					failures = append(failures, err)
				}
			}
		}
	}
	return true, errors.Join(failures...)
}

// Run joins its real owned publisher on every exit. Caller cancellation is not
// treated as proof that an enqueued Produce already stopped.
func (runner *BatchRunner) Run() {
	defer func() { _ = runner.Close() }()
	for runner.lifetime.Err() == nil {
		processed, err := runner.Poll()
		if runner.lifetime.Err() != nil || errors.Is(err, ErrRunnerClosed) {
			return
		}
		if err != nil {
			slog.Warn("community batch delivery is not complete", "event_id", runner.eventID(), "topic", runner.topic)
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-runner.lifetime.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (runner *BatchRunner) eventID() string {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.lastEvent
}

// Close joins Poll before closing the publisher, once. No new claim is allowed
// after it returns; the lifetime's owner still controls cancellation.
func (runner *BatchRunner) Close() error {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if !runner.closed {
		runner.closed = true
		runner.closeErr = runner.publisher.Close()
	}
	return runner.closeErr
}
