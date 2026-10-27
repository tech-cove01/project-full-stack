package social

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/communitycache"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// The suite owns one note, one user and their like row; concurrent callers are
// part of the fixture. Nothing global is truncated or reset.
func TestIntegrationExpectedStateLike(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("dependency cleanup failed")
		}
	})
	author := insertUser(t, resources, "135")
	reader := insertUser(t, resources, "136")
	note := insertNote(t, resources, author, "点赞集成测试", time.Now().Truncate(time.Second))
	userID := strconv.FormatUint(reader, 10)
	noteID := strconv.FormatUint(note, 10)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.ORM.WithContext(cleanupCtx).Exec(
			"DELETE FROM tb_community_outbox WHERE event_id LIKE ?", "LIKE:"+noteID+":%").Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec(
			"DELETE FROM tb_blog_like WHERE blog_id=?", note).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_blog WHERE id=?", note).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec(
			"DELETE FROM tb_user WHERE id IN (?,?)", author, reader).Error
	})
	service := NewService(resources.ORM).WithBitmap(communitycache.NewBitmap(resources.Redis))
	bitmap := communitycache.NewBitmap(resources.Redis)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.Redis.Del(cleanupCtx, "community:likes:"+noteID).Err()
	})

	if _, err = service.SetLike(ctx, userID, noteID, true); err != nil {
		t.Fatalf("like failed: %v", err)
	}
	// The bitmap projection must follow the committed fact.
	if count, present, err := bitmap.Count(ctx, note); err != nil || !present || count != 1 {
		exists, _ := resources.Redis.Exists(ctx, "community:likes:"+noteID).Result()
		t.Fatalf("the bitmap must hold the committed like: count=%d present=%v err=%v user=%d note=%d exists=%d",
			count, present, err, reader, note, exists)
	}
	state, err := service.LikeState(ctx, userID, noteID)
	if err != nil || state.Likes != 1 {
		t.Fatalf("the state read must use the bitmap: %+v %v", state, err)
	}
	// Repeating the same state must not bump the version or the counter.
	state, err = service.SetLike(ctx, userID, noteID, true)
	if err != nil || state.Version != 1 || state.Likes != 1 {
		t.Fatalf("a repeated like must be a no-op: %+v %v", state, err)
	}
	state, err = service.SetLike(ctx, userID, noteID, false)
	if err != nil || state.Liked || state.Likes != 0 || state.Version != 2 {
		t.Fatalf("unlike must decrement once: %+v %v", state, err)
	}
	state, err = service.SetLike(ctx, userID, noteID, false)
	if err != nil || state.Version != 2 || state.Likes != 0 {
		t.Fatalf("a repeated unlike must be a no-op: %+v %v", state, err)
	}

	// Ten concurrent likes converge to one active row, one counter and one event.
	var wait sync.WaitGroup
	results := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.SetLike(ctx, userID, noteID, true)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("a concurrent like failed: %v", err)
		}
	}
	final, err := service.LikeState(ctx, userID, noteID)
	if err != nil || !final.Liked || final.Likes != 1 || final.Version != 3 {
		t.Fatalf("concurrent likes must converge to one active like: %+v %v", final, err)
	}
	var events int64
	if err = resources.ORM.WithContext(ctx).Raw(`SELECT COUNT(*) FROM tb_community_outbox
		WHERE aggregate_id=? AND event_type='LIKE_CHANGED'`, noteID).Scan(&events).Error; err != nil {
		t.Fatal(err)
	}
	if events != 3 {
		t.Fatalf("one event per real change expected (like, unlike, like), got %d", events)
	}
	if _, err = service.SetLike(ctx, userID, "999999999999", true); !errors.Is(err, ErrNoteNotFound) {
		t.Fatalf("an unknown note must be rejected, got %v", err)
	}
}
