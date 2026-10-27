package communitycache

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// The suite owns one note row and its cache key; it deletes both.
func TestIntegrationNoteCountsCache(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	noteID := insertNoteFixture(t, resources)
	cache := New(resources.Redis, resources.ORM)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.Redis.Del(cleanupCtx, key(noteID)).Err()
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_blog WHERE id=?", noteID).Error
	})

	value, err := cache.Get(ctx, noteID)
	if err != nil || value.Likes != 0 || value.Comments != 0 {
		t.Fatalf("first read must come from MySQL: %+v %v", value, err)
	}
	// A direct database change must stay invisible while the cached copy is alive.
	if err = resources.ORM.WithContext(ctx).Exec("UPDATE tb_blog SET liked=7, comments=3 WHERE id=?", noteID).Error; err != nil {
		t.Fatal(err)
	}
	if value, err = cache.Get(ctx, noteID); err != nil || value.Likes != 0 {
		t.Fatalf("the cached copy must be served, got %+v %v", value, err)
	}
	if err = cache.Invalidate(ctx, noteID); err != nil {
		t.Fatal(err)
	}
	if value, err = cache.Get(ctx, noteID); err != nil || value.Likes != 7 || value.Comments != 3 {
		t.Fatalf("after invalidation the read must rebuild from MySQL: %+v %v", value, err)
	}

	// Twenty concurrent reads on a cold key must agree and must not error.
	if err = cache.Invalidate(ctx, noteID); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan Counts, 20)
	errors := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			loaded, err := cache.Get(ctx, noteID)
			if err != nil {
				errors <- err
				return
			}
			results <- loaded
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatalf("a concurrent read failed: %v", err)
	}
	for loaded := range results {
		if loaded.Likes != 7 || loaded.Comments != 3 {
			t.Fatalf("every concurrent read must see the same counters, got %+v", loaded)
		}
	}

	bulk, err := cache.GetMany(ctx, []uint64{noteID, 999999999})
	if err != nil || bulk[noteID].Likes != 7 {
		t.Fatalf("the batched read must return the known note: %+v %v", bulk, err)
	}
	if _, exists := bulk[999999999]; exists {
		t.Fatal("a missing note must not appear in the batch result")
	}
}

// A hot key is served from the in-process copy: deleting the Redis entry does not
// force a database read until the local copy expires.
func TestIntegrationLocalLayerAbsorbsHotKey(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	noteID := insertNoteFixture(t, resources)
	cache := New(resources.Redis, resources.ORM).WithLocalTTL(80 * time.Millisecond)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.Redis.Del(cleanupCtx, key(noteID)).Err()
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_blog WHERE id=?", noteID).Error
	})
	if _, err = cache.Get(ctx, noteID); err != nil {
		t.Fatal(err)
	}
	// Remove the Redis copy; the in-process entry must still answer.
	if err = resources.Redis.Del(ctx, key(noteID)).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = cache.Get(ctx, noteID); err != nil {
		t.Fatal("a hot key must still be served from the local layer")
	}
	// The local copy must expire, after which the read rebuilds the Redis key.
	time.Sleep(150 * time.Millisecond)
	if _, err = cache.Get(ctx, noteID); err != nil {
		t.Fatal(err)
	}
	exists, err := resources.Redis.Exists(ctx, key(noteID)).Result()
	if err != nil || exists != 1 {
		t.Fatalf("an expired local copy must fall through to Redis and rebuild it: %d %v", exists, err)
	}
}

func insertNoteFixture(t *testing.T, resources *database.Resources) uint64 {
	t.Helper()
	if err := resources.ORM.Exec(`INSERT INTO tb_blog
		(shop_id,user_id,title,images,content,status,publish_version)
		VALUES (0,1,'计数缓存集成测试','','正文','PUBLISHED',1)`).Error; err != nil {
		t.Fatal("note fixture insert failed")
	}
	var noteID uint64
	if err := resources.ORM.Raw("SELECT id FROM tb_blog ORDER BY id DESC LIMIT 1").Scan(&noteID).Error; err != nil || noteID == 0 {
		t.Fatal("note id unavailable")
	}
	return noteID
}
