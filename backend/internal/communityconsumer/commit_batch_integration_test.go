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

type countedReceiptProcessor struct {
	Processor
	batch                   IgnoredLikeBatchProcessor
	batchCalls, singleCalls int
}

func (processor *countedReceiptProcessor) Apply(ctx context.Context, projection string, message Message) error {
	processor.singleCalls++
	return processor.Processor.Apply(ctx, projection, message)
}

func (processor *countedReceiptProcessor) ApplyIgnoredLikes(ctx context.Context, projection string, messages []Message) error {
	processor.batchCalls++
	return processor.batch.ApplyIgnoredLikes(ctx, projection, messages)
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
	firstProcessor := &countedReceiptProcessor{Processor: f.runtime, batch: f.runtime}
	worker := f.worker(t, topic, "follow-v1", group, firstProcessor)
	first := attach(worker, true)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		_, err := worker.Poll(ctx)
		if err != nil {
			break
		}
	}
	if first.calls != 1 || len(worker.commitPending) != 4 || len(first.fetched) != 4 || firstProcessor.batchCalls != 1 || firstProcessor.singleCalls != 0 {
		t.Fatal("unconfirmed batch must retain exactly four durable source messages")
	}
	if worker.Close() != nil {
		t.Fatal("first reader failed to close before restart")
	}
	secondProcessor := &countedReceiptProcessor{Processor: f.runtime, batch: f.runtime}
	restarted, err := newWorkerWithGroup(cfg.Kafka.Brokers, topic, "follow-v1", group, secondProcessor)
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
	if len(second.fetched) != 8 || second.fetched[0] != 0 || second.calls != 2 || f.offset(t, topic, group) != 8 || secondProcessor.batchCalls != 2 || secondProcessor.singleCalls != 0 {
		t.Fatal("restart did not replay the durable prefix and commit complete batches")
	}
	var count int64
	if err = db.Raw(`SELECT COUNT(*) FROM tb_community_projection_event
		WHERE projection='follow-v1' AND event_id IN ? AND state='IGNORED'`, f.events).Scan(&count).Error; err != nil || count != 8 {
		t.Fatal("durable replay must keep exactly one receipt for every source event")
	}
}

func TestIntegrationIgnoredLikeReceiptBatchAtomicityAndRebuildFence(t *testing.T) {
	db, cfg := consumerDatabase(t)
	f := newConsumerFixture(t, db, cfg)
	message := func(version int, liked bool) Message {
		id := "LIKE:" + f.note + ":" + f.user + ":" + strconv.Itoa(version)
		f.events = append(f.events, id)
		source := encodedEvent(id, "LIKE_CHANGED", "BLOG", f.note, version,
			map[string]any{"blogId": f.note, "userId": f.user, "liked": liked, "version": version})
		source.Topic, source.Partition, source.Offset = "community.go072.receipt."+f.user, 0, int64(version)
		return sourceMessage(source)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, projection := range []string{communityprojection.Follow, communityprojection.Feed} {
		t.Run(projection, func(t *testing.T) {
			first, second, third, fourth := message(1, true), message(2, false), message(3, true), message(4, false)
			firstEvent, _ := communityprojection.Decode(first.Body)
			secondEvent, _ := communityprojection.Decode(second.Body)
			thirdEvent, _ := communityprojection.Decode(third.Body)
			fourthEvent, _ := communityprojection.Decode(fourth.Body)
			if err := f.runtime.ApplyIgnoredLikes(ctx, projection, []Message{first}); err != nil {
				t.Fatal("owned first receipt batch failed")
			}
			conflict := message(1, false)
			if err := f.runtime.ApplyIgnoredLikes(ctx, projection, []Message{second, conflict}); !errors.Is(err, communityprojection.ErrConflict) {
				t.Fatal("changed durable event hash was accepted")
			}
			f.scalar(t, `SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?`, 0, projection, secondEvent.EventID)
			var stored struct{ EventHash []byte }
			if err := db.Raw(`SELECT event_hash FROM tb_community_projection_event WHERE projection=? AND event_id=?`, projection, firstEvent.EventID).Scan(&stored).Error; err != nil || !bytes.Equal(stored.EventHash, firstEvent.Hash[:]) {
				t.Fatal("conflicting batch overwrote old canonical receipt")
			}
			for range 2 {
				if err := f.runtime.ApplyIgnoredLikes(ctx, projection, []Message{second, third, second}); err != nil {
					t.Fatal("valid duplicate batch did not commit idempotently")
				}
			}
			f.scalar(t, `SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id IN ? AND state='IGNORED'`, 3, projection, []string{firstEvent.EventID, secondEvent.EventID, thirdEvent.EventID})
			if err := db.Exec(`UPDATE tb_community_projection_event SET state='APPLIED' WHERE projection=? AND event_id=?`, projection, firstEvent.EventID).Error; err != nil {
				t.Fatal("cannot create owned state-conflict fixture")
			}
			if err := f.runtime.ApplyIgnoredLikes(ctx, projection, []Message{fourth, first}); !errors.Is(err, communityprojection.ErrConflict) {
				t.Fatal("wrong durable receipt state was accepted")
			}
			f.scalar(t, `SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?`, 0, projection, fourthEvent.EventID)
			if err := db.Exec(`UPDATE tb_community_projection_event SET state='IGNORED' WHERE projection=? AND event_id=?`, projection, firstEvent.EventID).Error; err != nil {
				t.Fatal("cannot restore exact owned state fixture")
			}
			lock := db.WithContext(ctx).Begin()
			if lock.Error != nil {
				t.Fatal("cannot acquire independent rebuild fence")
			}
			defer lock.Rollback()
			var control string
			if err := lock.Raw(`SELECT projection FROM tb_community_projection_control WHERE projection=? FOR UPDATE`, projection).Scan(&control).Error; err != nil || control != projection {
				t.Fatal("isolated control seed missing")
			}
			blocked, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			err := f.runtime.ApplyIgnoredLikes(blocked, projection, []Message{fourth})
			stop()
			if err == nil {
				t.Fatal("receipt batch crossed the existing rebuild control lock")
			}
			if lock.Rollback().Error != nil {
				t.Fatal("cannot release owned rebuild fence")
			}
			f.scalar(t, `SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?`, 0, projection, fourthEvent.EventID)
			if err := f.runtime.ApplyIgnoredLikes(ctx, projection, []Message{fourth}); err != nil {
				t.Fatal("receipt batch did not recover after fence release")
			}
		})
	}
}
