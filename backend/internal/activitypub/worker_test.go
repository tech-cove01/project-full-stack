package activitypub

import (
	"context"
	"testing"
	"time"
)

func TestWorkerStopsWithoutPollingACancelledLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { NewWorker(nil, nil).Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not honor application cancellation")
	}
}
