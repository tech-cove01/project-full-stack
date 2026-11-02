package communityrelay

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
)

// BatchPublishBytes is the opt-in lane's conservative multi-event claim budget.
// The Writer retains its original 1 MiB limit so a larger valid singleton does
// not lose the existing single-event publishing capacity.
const BatchPublishBytes = 256 << 10

const (
	batchWriterBytes = 1 << 20
	// A lane has one synchronous call, so no later call can fill a partial
	// partition batch while this one waits. Keep only a short SDK linger.
	batchWriterLinger = time.Millisecond
)

var (
	ErrInvalidPublisher = errors.New("invalid community batch publisher configuration")
	ErrPublisherBusy    = errors.New("community batch publisher already has an active operation")
	ErrPublisherClosed  = errors.New("community batch publisher is closed")
)

// BatchPublisher owns one synchronous Writer for one runner lifetime. The
// default Worker does not use this opt-in component. An error acknowledges none
// of the caller's batch, even when Kafka may have accepted a subset; SQL rows
// must remain retryable under their original lease and attempt fences.
type BatchPublisher struct {
	lifetime  context.Context
	batchSize int
	writeWait time.Duration
	newWriter func() *kafka.Writer
	mu        sync.Mutex
	writer    *kafka.Writer
	closed    bool
	closeErr  error
}

func NewBatchPublisher(lifetime context.Context, brokers []string, topic string, batchSize int) (*BatchPublisher, error) {
	if lifetime == nil || len(brokers) == 0 || strings.TrimSpace(topic) == "" || batchSize < 1 || batchSize > MaxBatchSize {
		return nil, ErrInvalidPublisher
	}
	for _, broker := range brokers {
		if strings.TrimSpace(broker) == "" {
			return nil, ErrInvalidPublisher
		}
	}
	addresses := append([]string(nil), brokers...)
	return &BatchPublisher{
		lifetime: lifetime, batchSize: batchSize, writeWait: 10 * time.Second,
		newWriter: func() *kafka.Writer {
			return &kafka.Writer{
				Addr: kafka.TCP(addresses...), Topic: topic, Balancer: &kafka.Hash{},
				RequiredAcks: kafka.RequireAll, Async: false, MaxAttempts: 1,
				WriteTimeout: 5 * time.Second, BatchTimeout: batchWriterLinger,
				BatchSize: batchSize, BatchBytes: batchWriterBytes,
				AllowAutoTopicCreation: true,
			}
		},
	}, nil
}

func (publisher *BatchPublisher) messages(events []Event) ([]kafka.Message, error) {
	if len(events) < 1 || len(events) > publisher.batchSize {
		return nil, ErrInvalidBatch
	}
	remaining := uint64(BatchPublishBytes)
	messages := make([]kafka.Message, 0, len(events))
	for _, event := range events {
		if len(events) > 1 {
			size := eventEncodedUpperBound(event)
			if size > remaining {
				return nil, ErrInvalidBatch
			}
			remaining -= size
		}
		body, err := json.Marshal(envelope{
			EventID: event.EventID, EventType: event.EventType,
			AggregateType: event.AggregateType, AggregateID: event.AggregateID,
			Version: event.AggregateVersion, Payload: json.RawMessage(event.Payload),
		})
		if err != nil {
			return nil, err
		}
		messages = append(messages, kafka.Message{
			Key: []byte(event.AggregateID), Value: body,
			Headers: []kafka.Header{
				{Key: "eventType", Value: []byte(event.EventType)},
				{Key: "eventId", Value: []byte(event.EventID)},
				{Key: "version", Value: []byte(strconv.Itoa(event.AggregateVersion))},
			},
		})
	}
	return messages, nil
}

// Publish returns nil only after the entire input was synchronously
// acknowledged. On any Writer error it really closes/drains the owned Writer
// before returning; Close's result is never interpreted as an acknowledgement.
// A concurrent call is rejected instead of adding an application queue.
func (publisher *BatchPublisher) Publish(events []Event) error {
	if !publisher.mu.TryLock() {
		return ErrPublisherBusy
	}
	defer publisher.mu.Unlock()
	if publisher.closed {
		return ErrPublisherClosed
	}
	if err := publisher.lifetime.Err(); err != nil {
		return err
	}
	messages, err := publisher.messages(events)
	if err != nil {
		return err
	}
	if publisher.writer == nil {
		publisher.writer = publisher.newWriter()
	}
	ctx, cancel := context.WithTimeout(publisher.lifetime, publisher.writeWait)
	err = publisher.writer.WriteMessages(ctx, messages...)
	cancel()
	if err == nil {
		return nil
	}
	// WriteMessages may return before its background Produce settles. Holding
	// the lane until this real drain prevents an uncertain batch being followed
	// by another Claim/publish on a hidden partition queue.
	closeErr := publisher.writer.Close()
	publisher.writer = nil
	if closeErr != nil {
		return errors.Join(err, closeErr)
	}
	return err
}

// Close waits for any active Publish and closes its remaining owned Writer.
// It has no artificial timeout: a timed-out goroutine is not a completed drain.
func (publisher *BatchPublisher) Close() error {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if !publisher.closed {
		publisher.closed = true
		if publisher.writer != nil {
			publisher.closeErr = publisher.writer.Close()
			publisher.writer = nil
		}
	}
	return publisher.closeErr
}
