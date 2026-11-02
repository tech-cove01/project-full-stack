package communityrelay

import "context"

// MaxBatchBytes bounds the optional conservative claim budget. It is not a
// change to Kafka's delivery/acknowledgement settings or its actual message limit.
const MaxBatchBytes = 1 << 20

// ClaimBatchBounded leases a due prefix whose conservative encoded sizes fit
// maxBytes. Rows beyond the prefix are not leased or charged an attempt. Leading
// exhausted rows retain ClaimBatch's parking behavior without using byte budget.
// A first oversized event is returned alone to avoid starvation; its eventual
// publisher must still enforce the actual Kafka byte limit and original retry
// policy. This method never asserts that such a singleton fits maxBytes.
func (repository *Repository) ClaimBatchBounded(ctx context.Context, limit, maxBytes int) ([]Event, error) {
	if maxBytes < 1 || maxBytes > MaxBatchBytes {
		return nil, ErrInvalidBatch
	}
	return repository.claimBatch(ctx, limit, maxBytes)
}

func eventEncodedUpperBound(event Event) uint64 {
	return encodedUpperBound(uint64(len(event.Payload)), uint64(len(event.EventID)),
		uint64(len(event.EventType)), uint64(len(event.AggregateType)), uint64(len(event.AggregateID)))
}

// JSON can expand one input byte to a six-byte escape. String fields also occur
// in Kafka keys/headers; factor eight covers both uses. Fixed framing, field
// names and the version's integer text fit in the remaining 512-byte allowance.
// Saturation preserves an upper bound even for theoretical overflowing lengths.
func encodedUpperBound(payload, eventID, eventType, aggregateType, aggregateID uint64) uint64 {
	const maximum = ^uint64(0)
	total := uint64(512)
	for _, term := range []struct{ bytes, factor uint64 }{
		{payload, 6}, {eventID, 8}, {eventType, 8}, {aggregateType, 8}, {aggregateID, 8},
	} {
		if term.bytes > (maximum-total)/term.factor {
			return maximum
		}
		total += term.bytes * term.factor
	}
	return total
}
