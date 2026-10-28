package communitycache

import (
	"context"
	"errors"
	"github.com/redis/go-redis/v9"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func TestIntegrationLikeProjectionEmptyRebuild(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	note := insertNoteFixture(t, r)
	t.Cleanup(func() {
		_ = r.Redis.Del(context.Background(), likesKey(note), "community:likes:versions:"+strconv.FormatUint(note, 10)).Err()
		_ = r.ORM.Exec("DELETE FROM tb_blog WHERE id=?", note).Error
	})
	likes := NewLikes(r.Redis)
	if _, err = likes.Rebuild(ctx, note, nil); err != nil {
		t.Fatal(err)
	}
	count, present, err := likes.Count(ctx, note)
	if err != nil || !present || count != 0 {
		t.Fatalf("completed zero-like rebuild must be present with zero count: %d %v %v", count, present, err)
	}
}

type snapshotBarrier struct {
	reached, release chan struct{}
	used             atomic.Bool
}

func (hook *snapshotBarrier) DialHook(next redis.DialHook) redis.DialHook { return next }
func (hook *snapshotBarrier) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (hook *snapshotBarrier) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		if cmd.Name() == "evalsha" && len(args) > 1 && args[1] == replaceLikes.Hash() && hook.used.CompareAndSwap(false, true) {
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

func TestIntegrationLikeRebuildLocksFactSnapshot(t *testing.T) {
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
		_ = r.Redis.Del(context.Background(), likesKey(note), versionsKey(note)).Err()
		_ = r.ORM.Exec("DELETE FROM tb_blog WHERE id=?", note).Error
	})
	hook := &snapshotBarrier{reached: make(chan struct{}), release: make(chan struct{})}
	r.Redis.AddHook(hook)
	finished := make(chan error, 1)
	go func() { _, err := NewLikes(r.Redis).RebuildFromDatabase(ctx, r.ORM, note); finished <- err }()
	select {
	case <-hook.reached:
	case <-ctx.Done():
		t.Fatal("rebuild did not reach snapshot replacement")
	}
	// This is a real competing row update, not a mock expectation about SQL text.
	waitCtx, stop := context.WithTimeout(ctx, 200*time.Millisecond)
	err = r.ORM.WithContext(waitCtx).Exec("UPDATE tb_blog SET liked=liked+1 WHERE id=?", note).Error
	stop()
	close(hook.release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("snapshot replacement did not retain the note lock: %v", err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("rebuild did not release its row lock")
	}
}

func TestIntegrationLikeProjectionFences(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	note := insertNoteFixture(t, r)
	t.Cleanup(func() {
		_ = r.Redis.Del(context.Background(), likesKey(note), versionsKey(note)).Err()
		_ = r.ORM.Exec("DELETE FROM tb_blog_like WHERE blog_id=?", note).Error
		_ = r.ORM.Exec("DELETE FROM tb_blog WHERE id=?", note).Error
	})
	if err = r.ORM.Exec("INSERT INTO tb_blog_like(blog_id,user_id,active,version) VALUES(?,7,0,2),(?,11,1,1)", note, note).Error; err != nil {
		t.Fatal(err)
	}
	likes := NewLikes(r.Redis)
	if count, err := likes.RebuildFromDatabase(ctx, r.ORM, note); err != nil || count != 1 {
		t.Fatalf("locked rebuild: %d %v", count, err)
	}
	if changed, err := likes.Mark(ctx, note, 7, true, 1); err != nil || changed {
		t.Fatalf("old like must not overwrite rebuilt unlike: %v %v", changed, err)
	}
	if changed, err := likes.Mark(ctx, note, 11, false, 2); err != nil || !changed {
		t.Fatalf("new unlike must apply: %v %v", changed, err)
	}
	if changed, err := likes.Mark(ctx, note, 11, true, 1); err != nil || changed {
		t.Fatalf("late like must not overwrite new unlike: %v %v", changed, err)
	}
	if count, present, err := likes.Count(ctx, note); err != nil || !present || count != 0 {
		t.Fatalf("versioned projection should be complete at zero: %d %v %v", count, present, err)
	}
	if _, err = likes.Mark(ctx, note, 11, true, 3); err != nil {
		t.Fatal(err)
	}
	if err = r.Redis.Del(ctx, likesKey(note)).Err(); err != nil {
		t.Fatal(err)
	}
	if _, present, err := likes.Count(ctx, note); err != nil || present {
		t.Fatalf("lost set must not report a complete zero: %v %v", present, err)
	}
	if _, err = likes.Mark(ctx, note, 13, true, 1); err != nil {
		t.Fatal(err)
	}
	if _, present, err := likes.Count(ctx, note); err != nil || present {
		t.Fatalf("one incremental member cannot recertify a lost set: %v %v", present, err)
	}
	before, err := r.Redis.Dump(ctx, likesKey(note)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err = applyLike.Run(ctx, r.Redis, []string{likesKey(note), versionsKey(note)}, "14", "4", "bad-state").Err(); err == nil {
		t.Fatal("invalid state must reject before writes")
	}
	if err = replaceLikes.Run(ctx, r.Redis, []string{likesKey(note), versionsKey(note)}, "14", "1", "1", "15", "1", "bad-state").Err(); err == nil {
		t.Fatal("invalid late snapshot element must reject before deleting old projection")
	}
	after, err := r.Redis.Dump(ctx, likesKey(note)).Result()
	if err != nil || before != after {
		t.Fatal("invalid projection arguments partially changed the set")
	}
}
