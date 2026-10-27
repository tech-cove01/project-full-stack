package activitypub

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGateRejectsInvalidArgumentsBeforeRedis(t *testing.T) {
	gate := NewGate(nil)
	ctx := context.Background()
	begin := time.Date(2026, 10, 26, 8, 0, 0, 0, time.FixedZone("Shanghai", 8*3600))
	for _, id := range []string{"", "0", "01", "-1", "1:2", "18446744073709551616"} {
		if !errors.Is(gate.Prepare(ctx, id, 1, 10, begin, begin.Add(time.Hour)), ErrUnsafe) ||
			!errors.Is(gate.Open(ctx, id, 1), ErrUnsafe) || !errors.Is(gate.Close(ctx, id, 1), ErrUnsafe) {
			t.Fatal("invalid IDs must fail before using the Redis client")
		}
	}
	for _, version := range []int{-1, 0, 2147483648} {
		if !errors.Is(gate.Open(ctx, "1", version), ErrUnsafe) || !errors.Is(gate.Close(ctx, "1", version), ErrUnsafe) {
			t.Fatal("invalid versions must fail before Redis")
		}
	}
	if !errors.Is(gate.Prepare(ctx, "1", 1, -1, begin, begin.Add(time.Hour)), ErrUnsafe) ||
		!errors.Is(gate.Prepare(ctx, "1", 1, 10, begin, begin), ErrUnsafe) {
		t.Fatal("negative inventory and invalid windows must fail before Redis")
	}
}
