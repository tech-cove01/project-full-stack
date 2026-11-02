package communityconsumer

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

type batchFakeReader struct {
	*fakeReader
	batches [][]int64
	ack     func(context.Context, []kafka.Message) error
}

func (source *batchFakeReader) CommitMessages(ctx context.Context, messages ...kafka.Message) error {
	offsets := make([]int64, len(messages))
	for i, message := range messages {
		offsets[i] = message.Offset
	}
	source.batches = append(source.batches, offsets)
	if source.ack != nil {
		if err := source.ack(ctx, messages); err != nil {
			return err
		}
	}
	source.committed = append(source.committed, offsets...)
	return nil
}

func batchLike(offset int64) kafka.Message {
	note := strconv.FormatInt(1000+offset, 10)
	message := encodedEvent("LIKE:"+note+":1:1", "LIKE_CHANGED", "BLOG", note, 1,
		map[string]any{"blogId": note, "userId": "1", "liked": true, "version": 1})
	message.Topic, message.Partition, message.Offset = "private.topic", 2, offset
	return message
}

func batchFollow(offset int64) kafka.Message {
	message := encodedEvent("FOLLOW:1:42:1", "FOLLOW_CHANGED", "FOLLOW", "42", 1,
		map[string]any{"userId": "1", "followUserId": "42", "active": true, "version": 1})
	message.Topic, message.Partition, message.Offset = "private.topic", 2, offset
	return message
}

func commitBatchWorker(t *testing.T, size int, messages ...kafka.Message) (*Worker, *batchFakeReader, map[int64]bool) {
	t.Helper()
	source := &batchFakeReader{fakeReader: newFakeReader(messages...)}
	durable := make(map[int64]bool)
	worker := workerWithReader(source, "follow-v1", processorFuncs{
		apply: func(_ context.Context, _ string, message Message) error {
			durable[message.Offset] = true
			return nil
		}, park: func(context.Context, string, Message, string) error {
			t.Fatal("valid message must not park")
			return nil
		},
	})
	if worker.SetCommitBatch(size) != nil {
		t.Fatal("batch fixture rejected")
	}
	worker.commitFlushInterval = time.Second // count-bound tests are independent of scheduling stalls.
	source.ack = func(ctx context.Context, messages []kafka.Message) error {
		if _, bounded := ctx.Deadline(); !bounded {
			t.Fatal("batched commit must retain its timeout")
		}
		for _, message := range messages {
			if !durable[message.Offset] {
				t.Fatal("offset batch overtook a non-durable effect")
			}
		}
		return nil
	}
	return worker, source, durable
}

func TestCommitBatchRetainsEveryDurableOffsetBeforeOneSynchronousAck(t *testing.T) {
	worker, source, _ := commitBatchWorker(t, 3, batchLike(7), batchLike(8), batchLike(9), batchLike(10))
	if processed, err := worker.Poll(context.Background()); !processed || err != nil {
		t.Fatal("valid durable batch failed")
	}
	if source.fetched != 3 || !reflect.DeepEqual(source.batches, [][]int64{{7, 8, 9}}) || len(worker.commitPending) != 0 {
		t.Fatal("batch exceeded its bound or failed to retain the full contiguous proof")
	}
}

func TestCommitBatchFailureRetriesIdenticalProofWithoutApplyOrFetch(t *testing.T) {
	worker, source, durable := commitBatchWorker(t, 2, batchLike(7), batchLike(8), batchLike(9))
	ack := source.ack
	source.ack = func(context.Context, []kafka.Message) error { return errors.New("private broker diagnostic") }
	if _, err := worker.Poll(context.Background()); err == nil || source.fetched != 2 || len(worker.commitPending) != 2 || len(durable) != 2 {
		t.Fatal("unacknowledged batch was not retained")
	}
	worker.processor = processorFuncs{apply: func(context.Context, string, Message) error {
		t.Fatal("durable effects must not be reapplied while the same ACK retries")
		return nil
	}}
	source.ack = ack
	if _, err := worker.Poll(context.Background()); err != nil || source.fetched != 2 || len(worker.commitPending) != 0 || !reflect.DeepEqual(source.batches, [][]int64{{7, 8}, {7, 8}}) {
		t.Fatal("commit retry changed the source prefix or fetched beyond it")
	}
}

func TestCommitBatchFlushesPrefixBeforeNonLikeOriginalSingleMessage(t *testing.T) {
	worker, source, _ := commitBatchWorker(t, 3, batchLike(7), batchFollow(8), batchLike(9))
	if _, err := worker.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.fetched != 2 || !reflect.DeepEqual(source.batches, [][]int64{{7}, {8}}) {
		t.Fatal("non-LIKE barrier did not retain the original single-message ACK")
	}
}

func TestCommitBatchPrefixAckFailureKeepsValidNonLikeBarrierUnapplied(t *testing.T) {
	worker, source, durable := commitBatchWorker(t, 3, batchLike(7), batchFollow(8), batchLike(9))
	ack := source.ack
	source.ack = func(context.Context, []kafka.Message) error { return errors.New("unconfirmed prefix ACK") }
	if _, err := worker.Poll(context.Background()); err == nil || source.fetched != 2 || durable[8] || worker.pending == nil || worker.pending.Offset != 8 {
		t.Fatal("non-LIKE barrier overtook an unacknowledged durable prefix")
	}
	source.ack = ack
	if _, err := worker.Poll(context.Background()); err != nil || durable[8] || source.fetched != 2 {
		t.Fatal("prefix retry applied the barrier or fetched beyond it")
	}
	if _, err := worker.Poll(context.Background()); err != nil || !durable[8] || source.fetched != 2 || !reflect.DeepEqual(source.batches, [][]int64{{7}, {7}, {8}}) {
		t.Fatal("recovered prefix did not return the non-LIKE event to its single ACK")
	}
}

func TestCommitBatchValidLikeWithBadSourceRetainsSingleDurableParkBarrier(t *testing.T) {
	for _, badHeader := range []bool{false, true} {
		bad := batchLike(8)
		if badHeader {
			bad.Headers[1].Value = []byte("FOLLOW_CHANGED")
		} else {
			bad.Key = []byte("foreign-key")
		}
		if !batchableLike(bad) {
			t.Fatal("bad-source fixture must retain a strictly valid LIKE body")
		}
		worker, source, durable := commitBatchWorker(t, 3, batchLike(7), bad, batchLike(9))
		worker.processor = processorFuncs{apply: func(_ context.Context, _ string, message Message) error {
			if message.Offset == 8 {
				return errors.New("source key/header validation failed")
			}
			durable[message.Offset] = true
			return nil
		}, park: func(_ context.Context, _ string, message Message, _ string) error {
			if string(message.Key) != string(bad.Key) || !reflect.DeepEqual(message.Headers, sourceMessage(bad).Headers) {
				t.Fatal("parking changed the original failed key or headers")
			}
			durable[8] = true
			return nil
		}}
		for range 2 {
			if _, err := worker.Poll(context.Background()); err == nil || source.fetched != 2 || !reflect.DeepEqual(source.committed, []int64{7}) {
				t.Fatal("failed source was bypassed before durable parking")
			}
		}
		if _, err := worker.Poll(context.Background()); err != nil || source.fetched != 2 || !reflect.DeepEqual(source.batches, [][]int64{{7}, {8}}) || len(worker.commitPending) != 0 {
			t.Fatal("parked LIKE was batched or fetched past before its single ACK")
		}
	}
}

func TestCommitBatchDoesNotPassInvalidMessageUntilDurableParking(t *testing.T) {
	worker, source, durable := commitBatchWorker(t, 3, batchLike(7), kafkaSource(8), batchLike(9))
	parkAvailable := false
	worker.processor = processorFuncs{apply: func(_ context.Context, _ string, message Message) error {
		if message.Offset == 8 {
			return errors.New("invalid event")
		}
		durable[message.Offset] = true
		return nil
	}, park: func(_ context.Context, _ string, message Message, _ string) error {
		if !parkAvailable {
			return errors.New("unconfirmed parking")
		}
		durable[message.Offset] = true
		return nil
	}}
	for range 4 {
		if _, err := worker.Poll(context.Background()); err == nil || source.fetched != 2 || !reflect.DeepEqual(source.committed, []int64{7}) {
			t.Fatal("a failed message was bypassed or acknowledged without parking")
		}
	}
	parkAvailable = true
	if _, err := worker.Poll(context.Background()); err != nil || source.fetched != 2 || !reflect.DeepEqual(source.batches, [][]int64{{7}, {8}}) {
		t.Fatal("durable park did not preserve the single-message barrier")
	}
}

func TestCommitBatchCancellationKeepsUnacknowledgedDurableProofForReplay(t *testing.T) {
	worker, source, durable := commitBatchWorker(t, 3, batchLike(7), batchLike(8), batchLike(9))
	ctx, cancel := context.WithCancel(context.Background())
	worker.processor = processorFuncs{apply: func(_ context.Context, _ string, message Message) error {
		durable[message.Offset] = true
		if message.Offset == 8 {
			cancel()
		}
		return nil
	}}
	if _, err := worker.Poll(ctx); !errors.Is(err, context.Canceled) || source.fetched != 2 || len(source.committed) != 0 || len(worker.commitPending) != 1 || worker.pending == nil || !worker.durable {
		t.Fatal("cancellation discarded or committed a partial durable prefix")
	}
}

func TestCommitBatchShortFetchFlushDoesNotShortenApplyDeadline(t *testing.T) {
	worker, source, durable := commitBatchWorker(t, 2, batchLike(7))
	worker.commitFlushInterval = 10 * time.Millisecond
	worker.processor = processorFuncs{apply: func(ctx context.Context, _ string, message Message) error {
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) < time.Second {
			t.Fatal("flush interval leaked into the individual DB deadline")
		}
		time.Sleep(20 * time.Millisecond)
		durable[message.Offset] = true
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := worker.Poll(ctx); err != nil || !reflect.DeepEqual(source.batches, [][]int64{{7}}) {
		t.Fatal("short fetch deadline did not flush the underfilled durable batch")
	}
}

func TestCommitBatchCloseDoesNotRetryOrFetchAfterReaderShutdown(t *testing.T) {
	worker, source, _ := commitBatchWorker(t, 2, batchLike(7), batchLike(8), batchLike(9))
	source.ack = func(context.Context, []kafka.Message) error { return errors.New("unconfirmed ACK") }
	_, _ = worker.Poll(context.Background())
	if worker.Close() != nil {
		t.Fatal("owned reader close failed")
	}
	if _, err := worker.Poll(context.Background()); !errors.Is(err, io.EOF) || source.fetched != 2 || len(source.batches) != 1 || len(worker.commitPending) != 2 {
		t.Fatal("closed batch reader was retried or unacknowledged proof was discarded")
	}
}

func TestCommitBatchSetterRetainsStrictBounds(t *testing.T) {
	worker := workerWithReader(nil, "follow-v1", nil)
	if worker.commitBatchSize != 1 || worker.commitFlushInterval != 10*time.Millisecond {
		t.Fatal("default batching contract changed")
	}
	for _, size := range []int{0, -1, 65} {
		if worker.SetCommitBatch(size) == nil || worker.commitBatchSize != 1 {
			t.Fatal("invalid commit batch was accepted")
		}
	}
}
