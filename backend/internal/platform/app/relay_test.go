package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
)

func TestBatchRelayLanesBindActualWorkerLifetimeAndJoinBeforePools(t *testing.T) {
	resources := &testResources{closed: make(chan struct{})}
	entered := make(chan context.Context, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	create := func(workerCtx context.Context) (func(), error) {
		return func() {
			entered <- workerCtx
			<-workerCtx.Done()
			<-release // Represents the lane's real synchronous publisher drain.
			select {
			case <-resources.closed:
				t.Error("dependency pools closed before the owned lane drained")
			default:
			}
		}, nil
	}
	owned := startBackground(context.Background(), resources, communityBatchRelayLane(logger, create), communityBatchRelayLane(logger, create))
	var closeOnce sync.Once
	var closeErr error
	closeOwned := func() error {
		closeOnce.Do(func() { closeErr = owned.Close() })
		return closeErr
	}
	t.Cleanup(func() { _ = closeOwned() })
	for range 2 {
		select {
		case actual := <-entered:
			if actual.Done() == nil || actual.Err() != nil {
				t.Fatal("batch lane did not receive the actual cancellable worker context")
			}
		case <-time.After(time.Second):
			t.Fatal("batch lane construction did not run")
		}
	}
	closed := make(chan error, 1)
	go func() { closed <- closeOwned() }()
	select {
	case <-resources.closed:
		t.Fatal("cancelled lane was treated as already drained")
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-closed:
		if err != nil || resources.calls.Load() != 1 || output.Len() != 0 {
			t.Fatal("batch workers failed to join before their pools closed once")
		}
	case <-time.After(time.Second):
		t.Fatal("controlled batch lanes did not converge")
	}
}

func TestBatchRelayConstructionFailureIsVisibleWithoutPrivateText(t *testing.T) {
	for _, failed := range []bool{false, true} {
		var output bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&output, nil))
		lane := communityBatchRelayLane(logger, func(context.Context) (func(), error) {
			if failed {
				return nil, errors.New("private-broker-token")
			}
			return nil, nil
		})
		lane(context.Background())
		if !strings.Contains(output.String(), "level=ERROR") || !strings.Contains(output.String(), "community batch relay configuration is invalid") || strings.Contains(output.String(), "private-broker-token") {
			t.Fatal("failed lane construction was silent or exposed private configuration")
		}
	}
}

func TestManualInvalidRelayBatchFailsBeforeOpeningDependencies(t *testing.T) {
	for _, count := range []int{0, 65} {
		cfg := config.Config{Kafka: config.Kafka{Brokers: []string{"offline.invalid:9092"}}, CommunityRelayBatch: count}
		logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
		if err := Run(context.Background(), cfg, logger, "offline"); err == nil || err.Error() != "community relay batch configuration is invalid" {
			t.Fatal("manually invalid relay batch reached dependency initialization")
		}
	}
}
