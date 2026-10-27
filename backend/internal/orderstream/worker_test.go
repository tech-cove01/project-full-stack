package orderstream

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestCancelledWorkerDoesNotAccessDependencies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker := newWorker(nil, nil, "private", "private", "private")
	if processed, err := worker.Poll(ctx); processed || err == nil {
		t.Fatal("cancelled worker must return before reading Redis")
	}
	worker.Run(ctx)
}

func TestMessageIDsRemainCanonicalStringsWithSignedOrderRange(t *testing.T) {
	for _, test := range []struct {
		value         string
		signed, valid bool
	}{
		{"9223372036854775807", true, true}, {"9223372036854775808", true, false},
		{"18446744073709551615", false, true}, {"9007199254740993", true, true},
		{"01", true, false}, {"0", true, false}, {"-1", true, false}, {"1.0", true, false},
	} {
		message := redis.XMessage{Values: map[string]any{"id": test.value}}
		value, valid := field(message, "id", test.signed)
		if valid != test.valid || (valid && value != test.value) {
			t.Fatal("invalid or imprecise stream identifier accepted")
		}
	}
	if _, valid := field(redis.XMessage{Values: map[string]any{"id": float64(9007199254740993)}}, "id", true); valid {
		t.Fatal("numeric message IDs must not be coerced from floating point")
	}
}
