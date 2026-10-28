package communitycache

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/redis/go-redis/v9"
)

type cacheWriteBarrier struct {
	key              string
	reached, release chan struct{}
	used             atomic.Bool
}

func (hook *cacheWriteBarrier) DialHook(next redis.DialHook) redis.DialHook { return next }
func (hook *cacheWriteBarrier) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (hook *cacheWriteBarrier) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		if cmd.Name() == "set" && len(args) > 1 && args[1] == hook.key && hook.used.CompareAndSwap(false, true) {
			close(hook.reached)
			select {
			case <-hook.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return next(ctx, cmd)
	}
}

func TestIntegrationCountLoadCannotUndoInvalidation(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	note := insertNoteFixture(t, r)
	t.Cleanup(func() {
		_ = r.Redis.Del(context.Background(), key(note)).Err()
		_ = r.ORM.Exec("DELETE FROM tb_blog WHERE id=?", note).Error
	})
	hook := &cacheWriteBarrier{key: key(note), reached: make(chan struct{}), release: make(chan struct{})}
	r.Redis.AddHook(hook)
	cache := New(r.Redis, r.ORM).WithLocalTTL(0)
	loader := make(chan error, 1)
	go func() { _, err := cache.Get(ctx, note); loader <- err }()
	select {
	case <-hook.reached:
	case <-ctx.Done():
		t.Fatal("loader did not reach old-value publication")
	}
	if err = r.ORM.Exec("UPDATE tb_blog SET liked=7 WHERE id=?", note).Error; err != nil {
		close(hook.release)
		t.Fatal(err)
	}
	invalidated := make(chan error, 1)
	go func() { invalidated <- cache.Invalidate(ctx, note) }()
	// Observe whether invalidation completes while the old SET is paused. Both
	// schedules must converge after the loader and invalidator have drained.
	var invalidateErr error
	completed := false
	select {
	case invalidateErr = <-invalidated:
		completed = true
	case <-time.After(200 * time.Millisecond):
	}
	close(hook.release)
	if err = <-loader; err != nil {
		t.Fatal(err)
	}
	if !completed {
		invalidateErr = <-invalidated
	}
	if invalidateErr != nil {
		t.Fatal(invalidateErr)
	}
	value, err := cache.Get(ctx, note)
	if err != nil || value.Likes != 7 {
		t.Fatalf("paused old load undid completed invalidation: %+v %v", value, err)
	}
}

func TestIntegrationCancelledCountWaiterReturnsIndependently(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	note := insertNoteFixture(t, r)
	t.Cleanup(func() {
		_ = r.Redis.Del(context.Background(), key(note)).Err()
		_ = r.ORM.Exec("DELETE FROM tb_blog WHERE id=?", note).Error
	})
	hook := &cacheWriteBarrier{key: key(note), reached: make(chan struct{}), release: make(chan struct{})}
	r.Redis.AddHook(hook)
	cache := New(r.Redis, r.ORM)
	leader := make(chan error, 1)
	go func() { _, err := cache.Get(ctx, note); leader <- err }()
	select {
	case <-hook.reached:
	case <-ctx.Done():
		t.Fatal("leader did not reach paused publication")
	}
	waitCtx, stop := context.WithCancel(ctx)
	stop()
	waiter := make(chan error, 1)
	go func() { _, err := cache.Get(waitCtx, note); waiter <- err }()
	var returned error
	independent := false
	select {
	case returned = <-waiter:
		independent = true
	case <-time.After(200 * time.Millisecond):
	}
	close(hook.release)
	if err = <-leader; err != nil {
		t.Fatal(err)
	}
	if !independent {
		returned = <-waiter
	}
	if !independent || !errors.Is(returned, context.Canceled) {
		t.Fatalf("canceled waiter remained dependent on leader: independent=%v error=%v", independent, returned)
	}
}
