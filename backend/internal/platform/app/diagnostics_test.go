package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/communitycards"
)

type closeFunc func() error

func (close closeFunc) Close() error { return close() }

func TestDiagnosticListenerAndCardsStopBeforePoolsAndFailureStillClosesPools(t *testing.T) {
	var calls []string
	profileTimeout := errors.New("controlled profiler timeout")
	cardFailure := errors.New("controlled card close error")
	owned := applicationResources{
		diagnostics: closeFunc(func() error { calls = append(calls, "diagnostics"); return profileTimeout }),
		cards:       closeFunc(func() error { calls = append(calls, "cards"); return cardFailure }),
		pools:       closeFunc(func() error { calls = append(calls, "pools"); return nil }),
	}
	if err := owned.Close(); !errors.Is(err, profileTimeout) || !errors.Is(err, cardFailure) {
		t.Fatal("resource shutdown errors were lost")
	}
	if !reflect.DeepEqual(calls, []string{"diagnostics", "cards", "pools"}) {
		t.Fatal("diagnostic or card lifetime overlapped released dependency pools")
	}
}

func TestAssemblyFailureClosesCardsBeforePoolsWithoutDiagnostics(t *testing.T) {
	// Empty batches exercise cache lifetime without connecting either pool.
	redisPool := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = redisPool.Close() })
	cards := communitycards.New(context.Background(), redisPool, &gorm.DB{})
	t.Cleanup(func() { _ = cards.Close() })
	if _, err := cards.GetMany(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	closed := false
	owned := applicationResources{cards: cards, pools: closeFunc(func() error {
		closed = true
		if _, err := cards.GetMany(context.Background(), nil); !errors.Is(err, communitycards.ErrClosed) {
			t.Fatal("card operations still accepted when pools were released")
		}
		return nil
	})}
	if err := owned.Close(); err != nil || !closed {
		t.Fatal("partial application assembly did not release its resources")
	}
}
