package communityconsumer

import (
	"context"
	"errors"
	"time"
)

// IgnoredLikeBatchProcessor commits only validated LIKE IGNORED receipts in a
// single fenced DB transaction. Nil proves every supplied source is durable.
// Other processors retain the existing individual-transaction offset batch.
type IgnoredLikeBatchProcessor interface {
	ApplyIgnoredLikes(context.Context, string, []Message) error
}

func (worker *Worker) pollIgnoredLikeBatch(ctx context.Context, processor IgnoredLikeBatchProcessor) (bool, error) {
	if worker.receiptFallback {
		// Never fetch beyond a failed batch. Retain its complete raw messages,
		// and return each one to the original Apply/retry/Park/single ACK path.
		if worker.pending == nil {
			worker.pending = &worker.receiptPending[0]
		}
		processed, err := worker.pollOne(ctx, false, worker.readTimeout)
		if err == nil && worker.pending == nil {
			worker.receiptPending = worker.receiptPending[1:]
			if len(worker.receiptPending) == 0 {
				worker.receiptFallback = false
				worker.pending, worker.receiptBarrier = worker.receiptBarrier, nil
			}
		}
		return processed, err
	}
	if worker.pending != nil {
		return worker.pollOne(ctx, false, worker.readTimeout)
	}
	// A retained not-yet-durable batch (e.g. canceled fetch) is processed before
	// any further fetch. No successor is allowed to hide an unresolved gap.
	if len(worker.receiptPending) == 0 {
		var flushAt time.Time
		for len(worker.receiptPending) < worker.commitBatchSize {
			fetchTimeout := worker.readTimeout
			if !flushAt.IsZero() {
				fetchTimeout = time.Until(flushAt)
				if fetchTimeout <= 0 {
					break
				}
			}
			processed, err := worker.fetchPending(ctx, fetchTimeout)
			if err != nil {
				return len(worker.receiptPending) > 0, err
			}
			if !processed {
				break
			}
			if !batchableLike(*worker.pending) {
				worker.receiptBarrier, worker.pending = worker.pending, nil
				break
			}
			worker.receiptPending = append(worker.receiptPending, *worker.pending)
			worker.pending = nil
			if flushAt.IsZero() {
				flushAt = time.Now().Add(worker.commitFlushInterval)
			}
		}
	}
	if len(worker.receiptPending) == 0 {
		if worker.receiptBarrier == nil {
			return false, nil
		}
		worker.pending, worker.receiptBarrier = worker.receiptBarrier, nil
		return worker.pollOne(ctx, false, worker.readTimeout)
	}
	messages := make([]Message, len(worker.receiptPending))
	for index, source := range worker.receiptPending {
		messages[index] = sourceMessage(source)
	}
	applyCtx, cancel := context.WithTimeout(ctx, worker.applyTimeout)
	err := processor.ApplyIgnoredLikes(applyCtx, worker.projection, messages)
	cancel()
	if err != nil {
		worker.receiptFallback = true
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		return true, errors.New("community ignored LIKE batch not durable; retained for individual retry")
	}
	worker.commitPending, worker.receiptPending = worker.receiptPending, nil
	worker.pending, worker.receiptBarrier = worker.receiptBarrier, nil
	return worker.commitDurableBatch(ctx)
}
