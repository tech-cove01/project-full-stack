package communityconsumer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

type processorFuncs struct {
	apply func(context.Context, string, Message) error
	park  func(context.Context, string, Message, string) error
}

func (processor processorFuncs) Apply(ctx context.Context, projection string, message Message) error {
	return processor.apply(ctx, projection, message)
}
func (processor processorFuncs) Park(ctx context.Context, projection string, message Message, cause string) error {
	return processor.park(ctx, projection, message, cause)
}

type fakeReader struct {
	messages   []kafka.Message
	fetched    int
	committed  []int64
	commit     func(context.Context, kafka.Message) error
	closed     chan struct{}
	closeOnce  sync.Once
	closeCalls atomic.Int32
}

func newFakeReader(messages ...kafka.Message) *fakeReader {
	return &fakeReader{messages: messages, closed: make(chan struct{})}
}
func (source *fakeReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	select {
	case <-source.closed:
		return kafka.Message{}, io.EOF
	default:
	}
	if source.fetched < len(source.messages) {
		message := source.messages[source.fetched]
		source.fetched++
		return message, nil
	}
	select {
	case <-ctx.Done():
		return kafka.Message{}, ctx.Err()
	case <-source.closed:
		return kafka.Message{}, io.EOF
	}
}
func (source *fakeReader) CommitMessages(ctx context.Context, messages ...kafka.Message) error {
	if len(messages) != 1 {
		return errors.New("commit must contain exactly the proved message")
	}
	if source.commit != nil {
		if err := source.commit(ctx, messages[0]); err != nil {
			return err
		}
	}
	source.committed = append(source.committed, messages[0].Offset)
	return nil
}
func (source *fakeReader) Close() error {
	source.closeCalls.Add(1)
	source.closeOnce.Do(func() { close(source.closed) })
	return nil
}
func kafkaSource(offset int64) kafka.Message {
	return kafka.Message{Topic: "private.topic", Partition: 2, Offset: offset,
		Key: []byte("42"), Value: []byte(`{"eventId":"source"}`), Headers: []kafka.Header{{Key: "eventId", Value: []byte("source")}}}
}

func TestCommitFollowsDurableApplyInSourceOrder(t *testing.T) {
	source := newFakeReader(kafkaSource(7), kafkaSource(8))
	var operations []string
	durable := map[int64]bool{}
	processor := processorFuncs{apply: func(ctx context.Context, projection string, message Message) error {
		if projection != "follow-v1" || message.Topic != "private.topic" || message.Partition != 2 || string(message.Key) != "42" {
			t.Fatal("projection/source identity was not preserved")
		}
		if _, bounded := ctx.Deadline(); !bounded {
			t.Fatal("apply must have a deadline")
		}
		operations = append(operations, "apply")
		durable[message.Offset] = true
		return nil
	}, park: func(context.Context, string, Message, string) error {
		t.Fatal("successful event cannot park")
		return nil
	}}
	source.commit = func(ctx context.Context, message kafka.Message) error {
		if !durable[message.Offset] {
			t.Fatal("offset committed before its durable effect")
		}
		if _, bounded := ctx.Deadline(); !bounded {
			t.Fatal("commit must have a deadline")
		}
		operations = append(operations, "commit")
		return nil
	}
	worker := workerWithReader(source, "follow-v1", processor)
	for range 2 {
		if processed, err := worker.Poll(context.Background()); !processed || err != nil {
			t.Fatal("valid source could not apply and commit")
		}
	}
	if !reflect.DeepEqual(operations, []string{"apply", "commit", "apply", "commit"}) || !reflect.DeepEqual(source.committed, []int64{7, 8}) {
		t.Fatal("consumer did not retain durable source ordering")
	}
}

func TestThreeFailedAppliesRequireDurableParkAndNeverSkipAnUnprovedMessage(t *testing.T) {
	source := newFakeReader(kafkaSource(7), kafkaSource(8))
	applyCount, parkCount := 0, 0
	parkAvailable := false
	worker := workerWithReader(source, "feed-v1", processorFuncs{
		apply: func(_ context.Context, _ string, message Message) error {
			applyCount++
			// An adapter cannot corrupt the raw source that will later be parked.
			message.Body[0], message.Key[0], message.Headers[0].Value[0] = 'x', 'x', 'x'
			return errors.New("secret driver diagnostic")
		},
		park: func(_ context.Context, projection string, message Message, cause string) error {
			parkCount++
			if projection != "feed-v1" || cause != "apply_failed" || string(message.Body) != string(kafkaSource(7).Value) || string(message.Key) != "42" || string(message.Headers[0].Value) != "source" {
				t.Fatal("durable parking lost the original source or exposed diagnostics")
			}
			if !parkAvailable {
				return errors.New("database unavailable")
			}
			return nil
		},
	})
	for index := 0; index < 6; index++ {
		processed, err := worker.Poll(context.Background())
		if !processed || err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unknown durable result must remain an opaque retryable error")
		}
		if source.fetched != 1 || len(source.committed) != 0 {
			t.Fatal("an unparked failure was committed or a later source was fetched")
		}
	}
	if applyCount != 3 || parkCount != 4 {
		t.Fatal("bounded apply attempts did not switch to persistent parking retries")
	}
	parkAvailable = true
	if processed, err := worker.Poll(context.Background()); !processed || err != nil || source.fetched != 1 || !reflect.DeepEqual(source.committed, []int64{7}) {
		t.Fatal("the offset did not commit after durable parking recovered")
	}
}

func TestCommitFailureRetriesOnlyCurrentCommitBeforeFetchingLaterOffsets(t *testing.T) {
	source := newFakeReader(kafkaSource(7), kafkaSource(8))
	applyCount, commitCount := 0, 0
	source.commit = func(context.Context, kafka.Message) error {
		commitCount++
		if commitCount < 3 {
			return errors.New("coordinator unavailable")
		}
		return nil
	}
	worker := workerWithReader(source, "follow-v1", processorFuncs{
		apply: func(context.Context, string, Message) error { applyCount++; return nil },
		park: func(context.Context, string, Message, string) error {
			t.Fatal("durable effect must not park")
			return nil
		},
	})
	for range 2 {
		if _, err := worker.Poll(context.Background()); err == nil || source.fetched != 1 || applyCount != 1 || len(source.committed) != 0 {
			t.Fatal("unconfirmed commit advanced the source or repeated the effect")
		}
	}
	if _, err := worker.Poll(context.Background()); err != nil || source.fetched != 1 || applyCount != 1 {
		t.Fatal("current confirmed commit did not recover")
	}
	if _, err := worker.Poll(context.Background()); err != nil || source.fetched != 2 || applyCount != 2 {
		t.Fatal("later message remained blocked after confirmed commit")
	}
}

func TestCancellationAfterDurableEffectBeforeCommitReplaysWithoutSecondEffect(t *testing.T) {
	durable, effects := false, 0
	ctx, cancel := context.WithCancel(context.Background())
	first := newFakeReader(kafkaSource(7))
	processor := processorFuncs{apply: func(context.Context, string, Message) error {
		if !durable {
			durable = true
			effects++
		}
		cancel() // Controlled DB-effect-before-offset window, not an OS kill.
		return nil
	}, park: func(context.Context, string, Message, string) error {
		t.Fatal("successful duplicate cannot park")
		return nil
	}}
	worker := workerWithReader(first, "follow-v1", processor)
	if processed, err := worker.Poll(ctx); !processed || !errors.Is(err, context.Canceled) || len(first.committed) != 0 || effects != 1 {
		t.Fatal("cancellation did not preserve the durable-effect/uncommitted-offset window")
	}
	if err := worker.Close(); err != nil {
		t.Fatal("old reader did not release")
	}
	second := newFakeReader(kafkaSource(7)) // An uncommitted source reappears on restart.
	restarted := workerWithReader(second, "follow-v1", processor)
	if _, err := restarted.Poll(context.Background()); err != nil || effects != 1 || !reflect.DeepEqual(second.committed, []int64{7}) {
		t.Fatal("replay changed the durable effect twice or failed to commit the duplicate")
	}
}

func TestRunCancellationBoundsReadWorkAndRetryAndClosesReaderOnce(t *testing.T) {
	for _, phase := range []string{"read", "apply", "retry"} {
		t.Run(phase, func(t *testing.T) {
			source := newFakeReader()
			entered := make(chan struct{}, 1)
			if phase != "read" {
				source.messages = []kafka.Message{kafkaSource(7)}
			}
			worker := workerWithReader(source, "follow-v1", processorFuncs{
				apply: func(ctx context.Context, _ string, _ Message) error {
					entered <- struct{}{}
					if phase == "apply" {
						<-ctx.Done()
						return ctx.Err()
					}
					return errors.New("transient")
				},
				park: func(context.Context, string, Message, string) error { return errors.New("not durable") },
			})
			worker.retryDelay = time.Hour
			ctx, cancel := context.WithCancel(context.Background())
			finished := make(chan struct{})
			go func() { worker.Run(ctx); close(finished) }()
			if phase != "read" {
				select {
				case <-entered:
				case <-time.After(time.Second):
					cancel()
					t.Fatal("worker did not enter the controlled phase")
				}
			}
			cancel()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("consumer did not leave on cancellation")
			}
			_ = worker.Close()
			if source.closeCalls.Load() != 1 || len(source.committed) != 0 {
				t.Fatal("reader closed more than once or unknown result committed during shutdown")
			}
		})
	}
}

func TestApplyTimeoutIsBoundedAndLogsDoNotExposeDiagnostic(t *testing.T) {
	source := newFakeReader(kafkaSource(7))
	worker := workerWithReader(source, "feed-v1", processorFuncs{
		apply: func(ctx context.Context, _ string, _ Message) error {
			<-ctx.Done()
			return errors.New("PRIVATE body/password diagnostic")
		},
		park: func(context.Context, string, Message, string) error {
			return errors.New("PRIVATE persistence diagnostic")
		},
	})
	worker.applyTimeout, worker.retryDelay = 5*time.Millisecond, time.Millisecond
	for range 3 {
		if _, err := worker.Poll(context.Background()); err == nil {
			t.Fatal("timed-out work must not acquire durable proof")
		}
	}
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(original)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	worker.Run(ctx)
	if worker.attempts != 3 || strings.Contains(logs.String(), "PRIVATE") || len(source.committed) != 0 {
		t.Fatal("bounded work/log privacy/unproved offset contract failed")
	}
}

func TestConstructorUsesSeparateStableSynchronousGroupsAndRejectsUnknownProjection(t *testing.T) {
	processor := processorFuncs{}
	for _, projection := range []string{"follow-v1", "feed-v1"} {
		worker, err := NewWorker([]string{"127.0.0.1:9092"}, "community.events", projection, processor)
		if err != nil {
			t.Fatal("supported projection was rejected")
		}
		configuration := worker.reader.(*kafka.Reader).Config()
		if configuration.GroupID != "community."+projection || configuration.CommitInterval != 0 || configuration.StartOffset != kafka.FirstOffset || configuration.QueueCapacity != 1 {
			t.Fatal("production offset/buffering configuration was not explicit")
		}
		_ = worker.Close() // No Fetch has started a broker connection.
	}
	for _, projection := range []string{"", "likes-v1", "FOLLOW-V1"} {
		if _, err := NewWorker([]string{"127.0.0.1:9092"}, "community.events", projection, processor); err == nil {
			t.Fatal("unknown projection must return an error")
		}
	}
	if _, err := NewWorker([]string{""}, "community.events", "follow-v1", processor); err == nil {
		t.Fatal("empty broker must return an error")
	}
}
