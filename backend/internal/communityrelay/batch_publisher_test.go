package communityrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol"
	"github.com/segmentio/kafka-go/protocol/metadata"
	"github.com/segmentio/kafka-go/protocol/produce"
)

// These tests use the actual pinned Writer/Client batching, cancellation and
// Close implementation. Only the protocol RoundTripper is deterministic; no
// broker, socket or replacement fake WriteMessages implementation is involved.
type publisherProduce struct {
	partition int32
	acks      int16
	messages  []kafka.Message
	remaining time.Duration
}

type publisherTransport struct {
	mu            sync.Mutex
	partitions    int
	metadataCalls int
	calls         []publisherProduce
	metadataHook  func(context.Context) error
	produceHook   func(context.Context, publisherProduce) (int16, error)
}

func (transport *publisherTransport) RoundTrip(ctx context.Context, _ net.Addr, request protocol.Message) (protocol.Message, error) {
	switch request := request.(type) {
	case *metadata.Request:
		transport.mu.Lock()
		transport.metadataCalls++
		transport.mu.Unlock()
		if transport.metadataHook != nil {
			if err := transport.metadataHook(ctx); err != nil {
				return nil, err
			}
		}
		partitions := make([]metadata.ResponsePartition, transport.partitions)
		for i := range partitions {
			partitions[i].PartitionIndex = int32(i)
		}
		return &metadata.Response{Topics: []metadata.ResponseTopic{{Name: request.TopicNames[0], Partitions: partitions}}}, nil
	case *produce.Request:
		if len(request.Topics) != 1 || len(request.Topics[0].Partitions) != 1 {
			return nil, errors.New("unexpected SDK produce topology")
		}
		partition := &request.Topics[0].Partitions[0]
		call := publisherProduce{partition: partition.Partition, acks: request.Acks}
		if deadline, ok := ctx.Deadline(); ok {
			call.remaining = time.Until(deadline)
		}
		for {
			record, err := partition.RecordSet.Records.ReadRecord()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			key, err := protocol.ReadAll(record.Key)
			if err != nil {
				return nil, err
			}
			value, err := protocol.ReadAll(record.Value)
			if err != nil {
				return nil, err
			}
			message := kafka.Message{Key: append([]byte(nil), key...), Value: append([]byte(nil), value...)}
			for _, header := range record.Headers {
				message.Headers = append(message.Headers, kafka.Header{Key: header.Key, Value: append([]byte(nil), header.Value...)})
			}
			call.messages = append(call.messages, message)
		}
		transport.mu.Lock()
		transport.calls = append(transport.calls, call)
		transport.mu.Unlock()
		var code int16
		if transport.produceHook != nil {
			var err error
			code, err = transport.produceHook(ctx, call)
			if err != nil {
				return nil, err
			}
		}
		return &produce.Response{Topics: []produce.ResponseTopic{{Topic: request.Topics[0].Topic,
			Partitions: []produce.ResponsePartition{{Partition: call.partition, ErrorCode: code}}}}}, nil
	default:
		return nil, fmt.Errorf("unexpected SDK request %T", request)
	}
}

func (transport *publisherTransport) snapshot() (int, []publisherProduce) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.metadataCalls, append([]publisherProduce(nil), transport.calls...)
}

type publisherWriterHistory struct {
	mu      sync.Mutex
	writers []*kafka.Writer
}

func (history *publisherWriterHistory) snapshot() []*kafka.Writer {
	history.mu.Lock()
	defer history.mu.Unlock()
	return append([]*kafka.Writer(nil), history.writers...)
}

func newSDKPublisher(t *testing.T, lifetime context.Context, size int, transport *publisherTransport) (*BatchPublisher, *publisherWriterHistory) {
	t.Helper()
	publisher, err := NewBatchPublisher(lifetime, []string{"offline.invalid:9092"}, "owned-offline-topic", size)
	if err != nil {
		t.Fatal(err)
	}
	history := new(publisherWriterHistory)
	create := publisher.newWriter
	publisher.newWriter = func() *kafka.Writer {
		writer := create()
		writer.Transport = transport
		history.mu.Lock()
		history.writers = append(history.writers, writer)
		history.mu.Unlock()
		return writer
	}
	t.Cleanup(func() { _ = publisher.Close() })
	return publisher, history
}

func publisherEvents(count int) []Event {
	events := make([]Event, count)
	for i := range events {
		events[i] = boundedEvent(uint64(i+1), "quoted <>& 汉字")
		events[i].AggregateID = strconv.Itoa(i)
		events[i].AggregateVersion = i + 1
	}
	return events
}

func waitPublisherResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("controlled SDK publisher did not converge")
		return nil
	}
}

func TestBatchPublisherPolicyAndDependencyFreePreflight(t *testing.T) {
	ctx := context.Background()
	for _, check := range []struct {
		ctx     context.Context
		brokers []string
		topic   string
		count   int
	}{{nil, []string{"b"}, "t", 1}, {ctx, nil, "t", 1}, {ctx, []string{" "}, "t", 1},
		{ctx, []string{"b"}, " ", 1}, {ctx, []string{"b"}, "t", 0}, {ctx, []string{"b"}, "t", 65}} {
		if publisher, err := NewBatchPublisher(check.ctx, check.brokers, check.topic, check.count); publisher != nil || !errors.Is(err, ErrInvalidPublisher) {
			t.Fatal("invalid publisher input must fail before constructing a dependency")
		}
	}
	transport := &publisherTransport{partitions: 1}
	publisher, history := newSDKPublisher(t, ctx, 32, transport)
	policy := publisher.newWriter()
	if policy.RequiredAcks != kafka.RequireAll || policy.Async || policy.MaxAttempts != 1 || policy.BatchSize != 32 ||
		policy.BatchBytes != batchWriterBytes || policy.WriteTimeout != 5*time.Second || policy.BatchTimeout != time.Millisecond || publisher.writeWait != 10*time.Second {
		t.Fatal("opt-in transport policy changed durability, attempt, byte/write bounds or reviewed linger")
	}
	_ = policy.Close()
	before := len(history.snapshot())
	badPayload := publisherEvents(1)
	badPayload[0].Payload = []byte("not json")
	overBudget := publisherEvents(2)
	overBudget[0].Payload = []byte(`{"text":"` + strings.Repeat("x", BatchPublishBytes/6) + `"}`)
	for _, events := range [][]Event{nil, publisherEvents(33), badPayload, overBudget} {
		if publisher.Publish(events) == nil {
			t.Fatal("invalid count/payload/conservative multi-event byte bound was accepted")
		}
	}
	if len(history.snapshot()) != before {
		t.Fatal("invalid input created a Writer")
	}
	if err := publisher.Close(); err != nil || !errors.Is(publisher.Publish(publisherEvents(1)), ErrPublisherClosed) {
		t.Fatal("closed publisher accepted another operation")
	}
}

func TestBatchPublisherActualSDKKeepsEnvelopeAndOneWireBatchPerPartition(t *testing.T) {
	transport := &publisherTransport{partitions: 2}
	publisher, history := newSDKPublisher(t, context.Background(), 64, transport)
	events := publisherEvents(64)
	if err := publisher.Publish(events); err != nil {
		t.Fatal(err)
	}
	_, calls := transport.snapshot()
	if len(calls) != 2 || len(history.snapshot()) != 1 {
		t.Fatal("bounded aggregate should create one owned Writer and one Produce per partition")
	}
	want := make(map[string]Event, len(events))
	for _, event := range events {
		want[event.EventID] = event
	}
	partitions := make(map[int32]bool)
	seen := make(map[string]bool)
	for _, call := range calls {
		if partitions[call.partition] || call.acks != int16(kafka.RequireAll) || call.remaining <= 0 || call.remaining > 5*time.Second {
			t.Fatal("actual SDK partition/RequiredAcks/Produce timeout contract changed")
		}
		partitions[call.partition] = true
		for _, message := range call.messages {
			var body envelope
			if err := json.Unmarshal(message.Value, &body); err != nil {
				t.Fatal(err)
			}
			event, ok := want[body.EventID]
			expected, _ := json.Marshal(envelope{EventID: event.EventID, EventType: event.EventType,
				AggregateType: event.AggregateType, AggregateID: event.AggregateID, Version: event.AggregateVersion,
				Payload: json.RawMessage(event.Payload)})
			if !ok || seen[body.EventID] || string(message.Value) != string(expected) || string(message.Key) != event.AggregateID ||
				!reflect.DeepEqual(message.Headers, []kafka.Header{{Key: "eventType", Value: []byte(event.EventType)},
					{Key: "eventId", Value: []byte(event.EventID)}, {Key: "version", Value: []byte(strconv.Itoa(event.AggregateVersion))}}) {
				t.Fatal("published key/envelope/header identity differs from the original event contract")
			}
			seen[body.EventID] = true
		}
	}
	if len(seen) != len(events) {
		t.Fatal("actual SDK aggregate omitted an event")
	}
}

func TestBatchPublisherActualSDKPartialFailureDrainsAndRecreates(t *testing.T) {
	transport := &publisherTransport{partitions: 2, produceHook: func(_ context.Context, call publisherProduce) (int16, error) {
		if call.partition == 0 {
			return int16(kafka.NotLeaderForPartition), nil // retriable, but MaxAttempts remains one.
		}
		return 0, nil
	}}
	publisher, history := newSDKPublisher(t, context.Background(), 32, transport)
	events := publisherEvents(32)
	err := publisher.Publish(events)
	var partial kafka.WriteErrors
	if !errors.As(err, &partial) || len(partial) != len(events) {
		t.Fatal("actual SDK mixed partition failure must leave the batch unacknowledged")
	}
	acknowledged, failed := 0, 0
	for _, result := range partial {
		if result == nil {
			acknowledged++
		} else {
			failed++
		}
	}
	_, calls := transport.snapshot()
	if acknowledged == 0 || failed == 0 || len(calls) != 2 || publisher.writer != nil {
		t.Fatal("partial errors caused retries, retained an uncertain Writer or erased actual mixed outcomes")
	}
	first := history.snapshot()[0]
	if !errors.Is(first.WriteMessages(context.Background(), kafka.Message{Value: []byte("closed")}), io.ErrClosedPipe) {
		t.Fatal("failed Writer was not really closed before Publish returned")
	}
	// The old Writer has fully settled. Only the next explicit live call may
	// construct another; replaying all original events preserves at-least-once.
	transport.produceHook = nil
	if err := publisher.Publish(events); err != nil || len(history.snapshot()) != 2 {
		t.Fatal("live lane did not recreate exactly once after the real error drain")
	}
}

func TestBatchPublisherActualSDKDeadlineWaitsForDrainBeforeReuse(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	transport := &publisherTransport{partitions: 1, produceHook: func(ctx context.Context, _ publisherProduce) (int16, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}}
	publisher, history := newSDKPublisher(t, context.Background(), 1, transport)
	publisher.writeWait = 25 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- publisher.Publish(publisherEvents(1)) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("actual SDK never began Produce")
	}
	select {
	case <-done:
		t.Fatal("caller deadline incorrectly pretended the background Produce had drained")
	case <-time.After(75 * time.Millisecond):
	}
	if !errors.Is(publisher.Publish(publisherEvents(1)), ErrPublisherBusy) || len(history.snapshot()) != 1 {
		t.Fatal("uncertain lane accepted another call or recreated before the old Produce drained")
	}
	releaseOnce.Do(func() { close(release) })
	if err := waitPublisherResult(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("drain success must not convert the expired actual write into an acknowledgement")
	}
	publisher.writeWait = time.Second
	if err := publisher.Publish(publisherEvents(1)); err != nil || len(history.snapshot()) != 2 {
		t.Fatal("a fully drained live lane could not subsequently recreate")
	}
}

func TestBatchPublisherActualSDKLifetimeCancellationAndCloseJoin(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
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
	publisher, history := newSDKPublisher(t, lifetime, 1, transport)
	done, closed := make(chan error, 1), make(chan error, 1)
	go func() { done <- publisher.Publish(publisherEvents(1)) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("actual SDK never began Produce")
	}
	cancel()
	go func() { closed <- publisher.Close() }()
	select {
	case <-closed:
		t.Fatal("Close did not join the unresolved actual SDK Produce")
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := waitPublisherResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal("lifetime cancellation was converted into an acknowledgement")
	}
	if err := waitPublisherResult(t, closed); err != nil || len(history.snapshot()) != 1 ||
		!errors.Is(publisher.Publish(publisherEvents(1)), ErrPublisherClosed) {
		t.Fatal("closing/cancelled lane reconstructed a Writer")
	}
}

func TestBatchPublisherActualSDKMetadataSharesFiniteCallerContext(t *testing.T) {
	transport := &publisherTransport{partitions: 1, metadataHook: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 50*time.Millisecond {
			return errors.New("metadata escaped the caller deadline")
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	lifetime, cancel := context.WithCancel(context.Background())
	publisher, history := newSDKPublisher(t, lifetime, 1, transport)
	publisher.writeWait = 25 * time.Millisecond
	if err := publisher.Publish(publisherEvents(1)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("actual SDK metadata did not receive the finite caller context")
	}
	metadataCalls, calls := transport.snapshot()
	cancel()
	if metadataCalls != 1 || len(calls) != 0 || !errors.Is(publisher.Publish(publisherEvents(1)), context.Canceled) || len(history.snapshot()) != 1 {
		t.Fatal("expired metadata produced a message or cancelled lifetime recreated its Writer")
	}
}

func TestBatchPublisherActualSDKSingletonUsesActualByteLimit(t *testing.T) {
	transport := &publisherTransport{partitions: 1}
	publisher, history := newSDKPublisher(t, context.Background(), 1, transport)
	loose := publisherEvents(1)
	loose[0].Payload = []byte(`{"text":"` + strings.Repeat("x", 300<<10) + `"}`)
	if eventEncodedUpperBound(loose[0]) <= BatchPublishBytes || publisher.Publish(loose) != nil {
		t.Fatal("a conservatively oversized but actually valid singleton was starved")
	}
	tooLarge := publisherEvents(1)
	tooLarge[0].Payload = []byte(`{"text":"` + strings.Repeat("x", batchWriterBytes) + `"}`)
	if publisher.Publish(tooLarge) == nil || publisher.writer != nil || len(history.snapshot()) != 1 {
		t.Fatal("actually oversized singleton bypassed the SDK byte limit or retained its failed Writer")
	}
	_, calls := transport.snapshot()
	if len(calls) != 1 || len(calls[0].messages[0].Value) <= 300<<10 {
		t.Fatal("the valid >300 KiB singleton was reduced or the >1 MiB singleton reached Produce")
	}
}

func TestBatchPublisherEightActualSDKLanesStayIndependentlyOwned(t *testing.T) {
	entered, release := make(chan struct{}, 8), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	transport := &publisherTransport{partitions: 1, produceHook: func(ctx context.Context, _ publisherProduce) (int16, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}}
	var publishers [8]*BatchPublisher
	var histories [8]*publisherWriterHistory
	var cancelled context.CancelFunc
	done := make(chan error, 8)
	for i := range publishers {
		lifetime := context.Background()
		if i == 0 {
			lifetime, cancelled = context.WithCancel(lifetime)
			defer cancelled()
		}
		publishers[i], histories[i] = newSDKPublisher(t, lifetime, 1, transport)
		go func(publisher *BatchPublisher) { done <- publisher.Publish(publisherEvents(1)) }(publishers[i])
	}
	for range publishers {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("eight independently owned lanes did not reach actual SDK Produce")
		}
	}
	writers := make(map[*kafka.Writer]bool)
	for _, history := range histories {
		owned := history.snapshot()
		if len(owned) != 1 || writers[owned[0]] {
			t.Fatal("independent lanes shared an SDK Writer")
		}
		writers[owned[0]] = true
	}
	cancelled()
	select {
	case <-done:
		t.Fatal("one lane's cancellation closed another or pretended its own Produce drained")
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	failures := 0
	for range publishers {
		if err := waitPublisherResult(t, done); err != nil {
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			failures++
		}
	}
	if failures != 1 || !errors.Is(publishers[0].Publish(publisherEvents(1)), context.Canceled) || len(histories[0].snapshot()) != 1 {
		t.Fatal("cancellation escaped its owned lane or allowed a cancelled lifetime to rebuild")
	}
}
