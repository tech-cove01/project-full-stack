package communityconsumer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

type receiptBatchProcessor struct {
	Processor
	batch func(context.Context, string, []Message) error
}

func (processor *receiptBatchProcessor) ApplyIgnoredLikes(ctx context.Context, projection string, messages []Message) error {
	return processor.batch(ctx, projection, messages)
}

func TestReceiptBatchCollectsBoundedRawPrefixBeforeOneDurableEffectAndAck(t *testing.T) {
	worker, source, durable := commitBatchWorker(t, 3, batchLike(7), batchLike(8), batchLike(9), batchLike(10))
	batchCalls := 0
	worker.processor = &receiptBatchProcessor{Processor: processorFuncs{
		apply: func(context.Context, string, Message) error {
			t.Fatal("LIKE batch fell through to individual DB commits")
			return nil
		},
	}, batch: func(ctx context.Context, projection string, messages []Message) error {
		batchCalls++
		if projection != "follow-v1" || len(messages) != 3 || source.fetched != 3 || len(durable) != 0 || len(source.batches) != 0 {
			t.Fatal("batch did not gather one bounded, unacknowledged raw prefix")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("receipt DB commit lost its bound")
		}
		for _, message := range messages {
			durable[message.Offset] = true
		}
		return nil
	}}
	if _, err := worker.Poll(context.Background()); err != nil || batchCalls != 1 || source.fetched != 3 || !reflect.DeepEqual(source.batches, [][]int64{{7, 8, 9}}) {
		t.Fatal("receipt batch was not made durable before its one synchronous ACK")
	}
}

func TestReceiptBatchAckFailureRetriesOnlySameDurablePrefix(t *testing.T) {
	worker, source, durable := commitBatchWorker(t, 2, batchLike(7), batchLike(8), batchLike(9))
	batchCalls := 0
	worker.processor = &receiptBatchProcessor{Processor: worker.processor, batch: func(_ context.Context, _ string, messages []Message) error {
		batchCalls++
		for _, message := range messages {
			durable[message.Offset] = true
		}
		return nil
	}}
	ack := source.ack
	source.ack = func(context.Context, []kafka.Message) error { return errors.New("uncertain ACK") }
	if _, err := worker.Poll(context.Background()); err == nil || batchCalls != 1 || len(worker.commitPending) != 2 {
		t.Fatal("durable receipts did not survive uncertain ACK")
	}
	source.ack = ack
	if _, err := worker.Poll(context.Background()); err != nil || batchCalls != 1 || source.fetched != 2 || !reflect.DeepEqual(source.batches, [][]int64{{7, 8}, {7, 8}}) {
		t.Fatal("ACK retry reapplied or fetched beyond its durable batch")
	}
}

func TestReceiptBatchRollbackKeepsFullRawOrderThroughOriginalParkAndNonLikeBarrier(t *testing.T) {
	bad := batchLike(8)
	bad.Key = []byte("original-invalid-key")
	worker, source, durable := commitBatchWorker(t, 4, batchLike(7), bad, batchLike(9), batchFollow(10), batchLike(11))
	var applied []int64
	worker.processor = &receiptBatchProcessor{Processor: processorFuncs{
		apply: func(_ context.Context, _ string, message Message) error {
			applied = append(applied, message.Offset)
			if message.Offset == 8 {
				return errors.New("source key conflict")
			}
			durable[message.Offset] = true
			return nil
		}, park: func(_ context.Context, _ string, message Message, _ string) error {
			if message.Offset != 8 || string(message.Key) != string(bad.Key) {
				t.Fatal("fallback parking lost immutable source")
			}
			durable[8] = true
			return nil
		},
	}, batch: func(_ context.Context, _ string, messages []Message) error {
		if len(messages) != 3 || messages[1].Offset != 8 || string(messages[1].Key) != string(bad.Key) {
			t.Fatal("batch lost its raw invalid source")
		}
		return errors.New("rolled-back receipt transaction")
	}}
	if _, err := worker.Poll(context.Background()); err == nil || len(source.batches) != 0 || source.fetched != 4 || len(worker.receiptPending) != 3 {
		t.Fatal("failed DB batch was acknowledged or its prefix discarded")
	}
	if _, err := worker.Poll(context.Background()); err != nil || !reflect.DeepEqual(source.committed, []int64{7}) {
		t.Fatal("fallback did not start with original first message")
	}
	for range 2 {
		if _, err := worker.Poll(context.Background()); err == nil || source.fetched != 4 || !reflect.DeepEqual(source.committed, []int64{7}) {
			t.Fatal("fallback crossed an undurable source gap")
		}
	}
	for range 3 {
		if _, err := worker.Poll(context.Background()); err != nil {
			t.Fatal("durable parking/fallback barrier failed")
		}
	}
	if source.fetched != 4 || !reflect.DeepEqual(source.batches, [][]int64{{7}, {8}, {9}, {10}}) || !reflect.DeepEqual(applied, []int64{7, 8, 8, 8, 9, 10}) {
		t.Fatal("raw fallback order or non-LIKE single ACK changed")
	}
}

func TestReceiptBatchCanceledAfterDBCommitRetainsProofWithoutAck(t *testing.T) {
	worker, source, durable := commitBatchWorker(t, 2, batchLike(7), batchLike(8), batchLike(9))
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	worker.processor = &receiptBatchProcessor{Processor: worker.processor, batch: func(_ context.Context, _ string, messages []Message) error {
		calls++
		for _, message := range messages {
			durable[message.Offset] = true
		}
		cancel()
		return nil
	}}
	if _, err := worker.Poll(ctx); !errors.Is(err, context.Canceled) || len(source.batches) != 0 || len(worker.commitPending) != 2 {
		t.Fatal("cancel erased a committed receipt batch or ACKed after cancel")
	}
	if _, err := worker.Poll(context.Background()); err != nil || calls != 1 || source.fetched != 2 {
		t.Fatal("durable cancellation replay repeated DB effects or fetched beyond its proof")
	}
}

func TestReceiptBatchCanceledWithoutDurableProofFallsBackWithoutFetching(t *testing.T) {
	worker, source, _ := commitBatchWorker(t, 2, batchLike(7), batchLike(8), batchLike(9))
	ctx, cancel := context.WithCancel(context.Background())
	worker.processor = &receiptBatchProcessor{Processor: worker.processor, batch: func(context.Context, string, []Message) error { cancel(); return context.Canceled }}
	if _, err := worker.Poll(ctx); !errors.Is(err, context.Canceled) || len(source.batches) != 0 || len(worker.receiptPending) != 2 {
		t.Fatal("canceled transaction lost unconfirmed raw prefix")
	}
	if _, err := worker.Poll(context.Background()); err != nil || source.fetched != 2 || !reflect.DeepEqual(source.committed, []int64{7}) {
		t.Fatal("canceled batch did not return to original single-message retry")
	}
}

func TestReceiptBatchUnderfilledFetchFlushRetainsIndependentDBDeadline(t *testing.T) {
	worker, source, durable := commitBatchWorker(t, 3, batchLike(7))
	worker.commitFlushInterval = 10 * time.Millisecond
	worker.processor = &receiptBatchProcessor{Processor: worker.processor, batch: func(ctx context.Context, _ string, messages []Message) error {
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) < time.Second || len(messages) != 1 {
			t.Fatal("10ms fetch budget leaked into DB commit deadline")
		}
		durable[7] = true
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := worker.Poll(ctx); err != nil || !reflect.DeepEqual(source.batches, [][]int64{{7}}) {
		t.Fatal("underfilled raw receipt batch did not flush")
	}
}
