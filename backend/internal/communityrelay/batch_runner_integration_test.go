package communityrelay

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol"
	"github.com/segmentio/kafka-go/protocol/produce"
)

func newRealBatchRunner(t *testing.T, fixture *batchFixture) (*BatchRunner, *kafka.Reader, context.CancelFunc) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil || len(cfg.Kafka.Brokers) == 0 {
		t.Fatal("registered local Kafka configuration is required")
	}
	topic := "community.events.it.batch." + randomSuffix(t)
	connection, err := kafka.DialContext(fixture.ctx, "tcp", cfg.Kafka.Brokers[0])
	if err != nil {
		t.Fatal("registered local broker is unavailable")
	}
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	err = connection.CreateTopics(kafka.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1})
	_ = connection.Close()
	if err != nil {
		t.Fatal("owned batch topic creation failed")
	}
	t.Cleanup(func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		owned, err := kafka.DialContext(ctx, "tcp", cfg.Kafka.Brokers[0])
		if err != nil {
			t.Error("owned batch topic cleanup connection failed")
			return
		}
		defer func() { _ = owned.Close() }()
		_ = owned.SetDeadline(time.Now().Add(4 * time.Second))
		if err := owned.DeleteTopics(topic); err != nil {
			t.Error("exact owned batch topic cleanup failed")
		}
	})
	lifetime, cancel := context.WithCancel(fixture.ctx)
	t.Cleanup(cancel)
	runner, err := NewBatchRunner(lifetime, fixture.resources.ORM, cfg.Kafka.Brokers, topic, 32)
	if err != nil {
		t.Fatal("registered batch runner construction failed")
	}
	t.Cleanup(func() { _ = runner.Close() })
	reader := kafka.NewReader(kafka.ReaderConfig{Brokers: cfg.Kafka.Brokers, Topic: topic,
		Partition: 0, MinBytes: 1, MaxBytes: 2 << 20})
	t.Cleanup(func() { _ = reader.Close() })
	return runner, reader, cancel
}

func readRealBatch(t *testing.T, ctx context.Context, reader *kafka.Reader, count int) map[string]int {
	t.Helper()
	read, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ids := make(map[string]int)
	for range count {
		message, err := reader.ReadMessage(read)
		if err != nil {
			t.Fatal("acknowledged owned batch message was not broker-readable")
		}
		var body envelope
		if err := json.Unmarshal(message.Value, &body); err != nil || body.EventID == "" ||
			kafkaHeader(message, "eventId") != body.EventID || string(message.Key) != body.AggregateID {
			t.Fatal("broker message lost original envelope/key/header identity")
		}
		ids[body.EventID]++
	}
	return ids
}

func TestIntegrationBatchRunnerActualAcknowledgementsAndCancellationClose(t *testing.T) {
	f := newBatchFixture(t)
	f.seed(t, 6)
	runner, reader, cancel := newRealBatchRunner(t, f)
	if processed, err := runner.Poll(); !processed || err != nil {
		t.Fatal("real broker acknowledgement did not complete the owned SQL batch")
	}
	for _, state := range f.states(t) {
		if state.Status != "DONE" || state.Attempts != 1 || state.Leased || !state.Completed {
			t.Fatal("actual acknowledged batch did not retain fenced durable completion")
		}
	}
	seen := readRealBatch(t, f.ctx, reader, len(f.ids))
	for _, id := range f.ids {
		if seen[id] != 1 {
			t.Fatal("real owned batch was omitted or published more than once without retry")
		}
	}
	if processed, err := runner.Poll(); processed || err != nil {
		t.Fatal("completed batch was reclaimed")
	}
	cancel()
	done := make(chan error, 1)
	go func() { runner.Run(); done <- nil }()
	if waitPublisherResult(t, done) != nil || runner.Close() != nil {
		t.Fatal("cancelled real runner did not join its owned publisher")
	}
	if processed, err := runner.Poll(); processed || !errors.Is(err, ErrRunnerClosed) {
		t.Fatal("closed real runner claimed again")
	}
}

// This adapter really delivers to the registered broker, then loses one
// successful response. It models acknowledgement uncertainty, not a fake ACK.
type runnerLostAcknowledgement struct {
	base kafka.RoundTripper
	lost atomic.Bool
}

func (transport *runnerLostAcknowledgement) RoundTrip(ctx context.Context, addr net.Addr, request protocol.Message) (protocol.Message, error) {
	response, err := transport.base.RoundTrip(ctx, addr, request)
	if err != nil {
		return nil, err
	}
	if _, producing := request.(*produce.Request); producing {
		actual, ok := response.(*produce.Response)
		if ok && len(actual.Topics) == 1 && len(actual.Topics[0].Partitions) == 1 &&
			actual.Topics[0].Partitions[0].ErrorCode == 0 && transport.lost.CompareAndSwap(false, true) {
			return nil, context.DeadlineExceeded
		}
	}
	return response, nil
}

func TestIntegrationBatchRunnerLostAcknowledgementRetriesWholeBatchAndReplays(t *testing.T) {
	f := newBatchFixture(t)
	f.seed(t, 3)
	runner, reader, _ := newRealBatchRunner(t, f)
	base := &kafka.Transport{DialTimeout: 3 * time.Second}
	t.Cleanup(base.CloseIdleConnections)
	lost := &runnerLostAcknowledgement{base: base}
	publisher := runner.publisher.(*BatchPublisher)
	create := publisher.newWriter
	var writers atomic.Int32
	publisher.newWriter = func() *kafka.Writer {
		writers.Add(1)
		writer := create()
		writer.Transport = lost
		return writer
	}
	if processed, err := runner.Poll(); !processed || err == nil || !lost.lost.Load() {
		t.Fatal("successful broker delivery with a lost response must remain uncertain")
	}
	for _, state := range f.states(t) {
		if state.Status != "PENDING" || state.Attempts != 1 || state.Leased || state.Completed {
			t.Fatal("uncertain batch inferred ACKs or bypassed original retry state")
		}
	}
	var future int
	if err := f.resources.ORM.Raw(`SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN ? AND next_attempt_at>NOW(3)`, f.ids).Scan(&future).Error; err != nil || future != len(f.ids) {
		t.Fatal("uncertain batch bypassed durable exponential retry backoff")
	}
	first := readRealBatch(t, f.ctx, reader, len(f.ids))
	if err := f.resources.ORM.Exec(`UPDATE tb_community_outbox SET next_attempt_at=DATE_SUB(NOW(3),INTERVAL 1 SECOND) WHERE event_id IN ?`, f.ids).Error; err != nil {
		t.Fatal("owned retry fixture could not be made due")
	}
	if processed, err := runner.Poll(); !processed || err != nil || writers.Load() != 2 {
		t.Fatal("drained retry did not reconstruct one Writer and complete the replay")
	}
	second := readRealBatch(t, f.ctx, reader, len(f.ids))
	for _, id := range f.ids {
		if first[id] != 1 || second[id] != 1 {
			t.Fatal("whole-batch uncertain delivery replay lost stable event identity")
		}
	}
	for _, state := range f.states(t) {
		if state.Status != "DONE" || state.Attempts != 2 || state.Leased || !state.Completed {
			t.Fatal("replayed batch did not preserve the second-attempt completion fence")
		}
	}
}

type runnerStaleCompletion struct {
	*Repository
	once   sync.Once
	expire func() error
}

func (repo *runnerStaleCompletion) DoneBatch(ctx context.Context, events []Event) error {
	var err error
	repo.once.Do(func() { err = repo.expire() })
	if err != nil {
		return err
	}
	return repo.Repository.DoneBatch(ctx, events)
}

func TestIntegrationBatchRunnerStaleLeaseSalvagesOnlyStillOwnedAcknowledgements(t *testing.T) {
	f := newBatchFixture(t)
	rows := f.seed(t, 3)
	runner, reader, _ := newRealBatchRunner(t, f)
	runner.repo = &runnerStaleCompletion{Repository: NewRepository(f.resources.ORM), expire: func() error {
		return f.resources.ORM.Exec(`UPDATE tb_community_outbox SET lease_until=DATE_SUB(NOW(3),INTERVAL 1 SECOND) WHERE id=?`, rows[1].ID).Error
	}}
	if processed, err := runner.Poll(); !processed || !errors.Is(err, ErrLeaseLost) {
		t.Fatal("stale middle lease was completed without its original fence")
	}
	states := f.states(t)
	if states[0].Status != "DONE" || states[1].Status != "PROCESSING" || states[1].Attempts != 1 || states[1].Completed || states[2].Status != "DONE" {
		t.Fatal("known stale rollback failed to salvage precisely the still-owned acknowledgements")
	}
	if processed, err := runner.Poll(); !processed || err != nil {
		t.Fatal("expired stale event could not be safely reclaimed and replayed")
	}
	seen := readRealBatch(t, f.ctx, reader, 4)
	for i, id := range f.ids {
		want := 1
		if i == 1 {
			want = 2
		}
		if seen[id] != want {
			t.Fatal("lease replay did not preserve the expected stable identity/duplicate contract")
		}
	}
	states = f.states(t)
	if states[1].Status != "DONE" || states[1].Attempts != 2 || states[1].Leased || !states[1].Completed {
		t.Fatal("replacement lease did not own the final completion")
	}
}

func TestIntegrationBatchRunnerOversizedEighthAttemptParksAndTailAdvances(t *testing.T) {
	f := newBatchFixture(t)
	rows := f.seed(t, 3)
	payload, _ := json.Marshal(map[string]string{"text": strings.Repeat("x", batchWriterBytes)})
	if err := f.resources.ORM.Exec(`UPDATE tb_community_outbox SET payload=?,attempts=7 WHERE id=?`, string(payload), rows[0].ID).Error; err != nil {
		t.Fatal("owned oversized retry fixture insert failed")
	}
	runner, reader, _ := newRealBatchRunner(t, f)
	if processed, err := runner.Poll(); !processed || err == nil {
		t.Fatal("actually oversized singleton bypassed the original SDK error path")
	}
	states := f.states(t)
	if states[0].Status != "NEEDS_REVIEW" || states[0].Attempts != 8 || states[0].Leased || states[0].Completed ||
		states[1].Status != "PENDING" || states[1].Attempts != 0 || states[2].Status != "PENDING" || states[2].Attempts != 0 {
		t.Fatal("eighth attempt was reset, lost review durability or charged the unselected tail")
	}
	if processed, err := runner.Poll(); !processed || err != nil {
		t.Fatal("parked oversized head starved the following valid byte prefix")
	}
	seen := readRealBatch(t, f.ctx, reader, 2)
	if len(seen) != 2 || seen[f.ids[1]] != 1 || seen[f.ids[2]] != 1 || seen[f.ids[0]] != 0 {
		t.Fatal("oversized head reached the broker or valid tail was lost")
	}
}
