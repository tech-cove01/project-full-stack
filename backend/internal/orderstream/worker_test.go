package orderstream

import (
	"context"
	"sync"
	"testing"
	"time"

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

func TestIndependentWorkerIdentitiesAndCancelledGroup(t *testing.T) {
	workers := make([]*Worker, 4)
	names := make(map[string]bool)
	for i := range workers {
		workers[i] = NewWorker(nil, nil)
		if names[workers[i].consumer] {
			t.Fatal("independent workers reused a consumer identity")
		}
		names[workers[i].consumer] = true
	}
	workers[0].retries["private-message"] = retryState{attempts: 1}
	for _, worker := range workers[1:] {
		if len(worker.retries) != 0 {
			t.Fatal("consumer retry state was shared across workers")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var wait sync.WaitGroup
	for _, worker := range workers {
		wait.Add(1)
		go func() { defer wait.Done(); worker.Run(ctx) }()
	}
	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled consumer group did not exit")
	}
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
