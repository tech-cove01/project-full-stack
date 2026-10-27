package communityrelay

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

// Worker publishes community events one at a time. At-least-once is the contract:
// a broker acknowledgement is required before the row is marked done, and a
// failure keeps the row retryable with backoff.
type Worker struct {
	repo      *Repository
	writer    *kafka.Writer
	topic     string
	lastEvent string
}

func NewWorker(db *gorm.DB, brokers []string, topic string) *Worker {
	return &Worker{
		repo:  NewRepository(db),
		topic: topic,
		writer: &kafka.Writer{
			Addr:         kafka.TCP(brokers...),
			Topic:        topic,
			Balancer:     &kafka.Hash{},
			RequiredAcks: kafka.RequireAll,
			WriteTimeout: 5 * time.Second,
			BatchTimeout: 10 * time.Millisecond,
			// The topic is created by the first event on a fresh broker; a relay
			// that cannot write must not crash the process.
			AllowAutoTopicCreation: true,
		},
	}
}

func (worker *Worker) Close() error { return worker.writer.Close() }

func (worker *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := worker.Poll(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// Only the event id is logged: driver text may include payload fragments.
			slog.Warn("community event delivery is not complete",
				"event_id", worker.lastEvent, "topic", worker.topic)
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type envelope struct {
	EventID       string          `json:"eventId"`
	EventType     string          `json:"eventType"`
	AggregateType string          `json:"aggregateType"`
	AggregateID   string          `json:"aggregateId"`
	Version       int             `json:"version"`
	Payload       json.RawMessage `json:"payload"`
}

func (worker *Worker) Poll(ctx context.Context) (bool, error) {
	event, err := worker.repo.Claim(ctx)
	if err != nil {
		return false, err
	}
	if event == nil {
		return false, nil
	}
	worker.lastEvent = event.EventID
	body, err := json.Marshal(envelope{
		EventID: event.EventID, EventType: event.EventType,
		AggregateType: event.AggregateType, AggregateID: event.AggregateID,
		Version: event.AggregateVersion, Payload: json.RawMessage(event.Payload),
	})
	if err != nil {
		return true, worker.repo.NeedsReview(ctx, *event, err)
	}
	message := kafka.Message{
		// The aggregate id is the partition key, so events of one note or one
		// relation keep their order.
		Key:   []byte(event.AggregateID),
		Value: body,
		Headers: []kafka.Header{
			{Key: "eventType", Value: []byte(event.EventType)},
			{Key: "eventId", Value: []byte(event.EventID)},
			{Key: "version", Value: []byte(strconv.Itoa(event.AggregateVersion))},
		},
	}
	if err = worker.writer.WriteMessages(ctx, message); err != nil {
		if event.Attempts >= maxAttempts {
			return true, worker.repo.NeedsReview(ctx, *event, err)
		}
		return true, worker.repo.Retry(ctx, *event, err)
	}
	return true, worker.repo.Done(ctx, *event)
}
