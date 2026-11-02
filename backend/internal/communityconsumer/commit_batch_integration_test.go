package communityconsumer

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/cityloop/fullstack-go/internal/communityprojection"
)

type receiptCommitReader struct {
	reader
	check    func(context.Context, []kafka.Message)
	failOnce bool
	calls    int
	fetched  []int64
}

func (source *receiptCommitReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	message, err := source.reader.FetchMessage(ctx)
	if err == nil {
		source.fetched = append(source.fetched, message.Offset)
	}
	return message, err
}

func (source *receiptCommitReader) CommitMessages(ctx context.Context, messages ...kafka.Message) error {
	source.calls++
	source.check(ctx, messages)
	if source.failOnce {
		source.failOnce = false
		return errors.New("injected synchronous offset ACK unavailable")
	}
	return source.reader.CommitMessages(ctx, messages...)
}

// This finite test uses exact owned SQL fixtures and a private Kafka topic/group.
// It must be run against isolated integration dependencies, never the CAP10 DB.
func TestIntegrationSynchronousCommitBatchReplaysDurableReceiptsAfterRestart(t *testing.T) {
	db, cfg := consumerDatabase(t)
	f := newConsumerFixture(t, db, cfg)
	topic := f.topic(t)
	group := "community.go072.batch." + f.user
	var messages []kafka.Message
	for version := 1; version <= 8; version++ {
		id := "LIKE:" + f.note + ":" + f.user + ":" + strconv.Itoa(version)
		f.events = append(f.events, id)
		messages = append(messages, encodedEvent(id, "LIKE_CHANGED", "BLOG", f.note, version,
			map[string]any{"blogId": f.note, "userId": f.user, "liked": version%2 == 1, "version": version}))
	}
	f.write(t, topic, messages...)
	check := func(ctx context.Context, messages []kafka.Message) {
		for _, message := range messages {
			event, err := communityprojection.Decode(message.Value)
			if err != nil {
				t.Fatal("owned LIKE event decode failed")
			}
			var row struct {
				EventHash []byte
				State     string
			}
			if err = db.WithContext(ctx).Raw(`SELECT event_hash,state FROM tb_community_projection_event
				WHERE projection='follow-v1' AND event_id=?`, event.EventID).Scan(&row).Error; err != nil || row.State != "IGNORED" || !bytes.Equal(row.EventHash, event.Hash[:]) {
				t.Fatal("synchronous offset commit preceded its durable exact receipt")
			}
		}
	}
	attach := func(worker *Worker, fail bool) *receiptCommitReader {
		if worker.SetCommitBatch(4) != nil {
			t.Fatal("finite commit batch configuration failed")
		}
		// This case verifies the count bound/replay; the unit suite separately
		// verifies production's 10ms underfilled-fetch flush.
		worker.commitFlushInterval = time.Second
		source := &receiptCommitReader{reader: kafka.NewReader(*worker.readerConfig), check: check, failOnce: fail}
		worker.reader, worker.ready, worker.topicReady = source, true, nil
		return source
	}
	worker := f.worker(t, topic, "follow-v1", group, f.runtime)
	first := attach(worker, true)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		_, err := worker.Poll(ctx)
		if err != nil {
			break
		}
	}
	if first.calls != 1 || len(worker.commitPending) != 4 || len(first.fetched) != 4 {
		t.Fatal("unconfirmed batch must retain exactly four durable source messages")
	}
	if worker.Close() != nil {
		t.Fatal("first reader failed to close before restart")
	}
	restarted, err := newWorkerWithGroup(cfg.Kafka.Brokers, topic, "follow-v1", group, f.runtime)
	if err != nil {
		t.Fatal("private restarted consumer setup failed")
	}
	t.Cleanup(func() { _ = restarted.Close() })
	second := attach(restarted, false)
	for ctx.Err() == nil && len(second.fetched) < 8 {
		if _, err = restarted.Poll(ctx); err != nil {
			t.Fatal("private restarted batch failed")
		}
	}
	if len(second.fetched) != 8 || second.fetched[0] != 0 || second.calls != 2 || f.offset(t, topic, group) != 8 {
		t.Fatal("restart did not replay the durable prefix and commit complete batches")
	}
	var count int64
	if err = db.Raw(`SELECT COUNT(*) FROM tb_community_projection_event
		WHERE projection='follow-v1' AND event_id IN ? AND state='IGNORED'`, f.events).Scan(&count).Error; err != nil || count != 8 {
		t.Fatal("durable replay must keep exactly one receipt for every source event")
	}
}
