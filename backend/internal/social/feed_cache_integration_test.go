package social

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/communitycache"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// A single connection makes the old nested-pool dependency deterministic: the
// snapshot owns it, and a cold GetMany cannot borrow another until it returns.
// This test uses existing public APIs so the same test also runs on old feed.go
// through a Go overlay, without changing the current working tree.
func TestIntegrationFeedColdCountsWithOneConnection(t *testing.T) {
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
	users, notes := make([]uint64, 0, 2), make([]uint64, 0, 2)
	var followerID, authorID string
	cache := communitycache.New(resources.Redis, resources.ORM).WithLocalTTL(0)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, id := range notes {
			if cache.Invalidate(cleanupCtx, id) != nil {
				t.Error("owned counter cleanup failed")
			}
		}
		if followerID != "" && authorID != "" {
			if resources.ORM.WithContext(cleanupCtx).Exec(`DELETE FROM tb_community_outbox
				WHERE aggregate_type='FOLLOW' AND event_id LIKE ?`, "FOLLOW:"+followerID+":"+authorID+":%").Error != nil {
				t.Error("owned follow event cleanup failed")
			}
		}
		for _, id := range notes {
			if resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_blog WHERE id=?", id).Error != nil {
				t.Error("owned note cleanup failed")
			}
		}
		for _, id := range users {
			if resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_follow WHERE user_id=?", id).Error != nil ||
				resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_user WHERE id=?", id).Error != nil {
				t.Error("owned user cleanup failed")
			}
		}
	})
	follower := insertUser(t, resources, "135")
	users = append(users, follower)
	followerID = strconv.FormatUint(follower, 10)
	author := insertUser(t, resources, "134")
	users = append(users, author)
	authorID = strconv.FormatUint(author, 10)
	created := time.Now().Truncate(time.Second)
	first := insertNote(t, resources, author, "cold counts first", created)
	notes = append(notes, first)
	second := insertNote(t, resources, author, "cold counts second", created)
	notes = append(notes, second)
	service := NewService(resources.ORM).WithProjections().WithCounts(cache)
	if _, err = service.SetFollow(ctx, followerID, authorID, true); err != nil {
		t.Fatal("owned follow setup failed")
	}
	for _, id := range notes {
		if err = cache.Invalidate(ctx, id); err != nil {
			t.Fatal("cannot establish cold owned counters")
		}
	}
	resources.SQL.SetMaxOpenConns(1)
	resources.SQL.SetMaxIdleConns(1)
	waitsBefore := resources.SQL.Stats().WaitCount
	feedCtx, stopFeed := context.WithTimeout(ctx, 3*time.Second)
	page, err := service.Feed(feedCtx, followerID, "", "", 1)
	stopFeed()
	if err != nil {
		t.Fatalf("cold Feed must finish with one SQL connection: %v (pool waits %d)",
			err, resources.SQL.Stats().WaitCount-waitsBefore)
	}
	if page.Source != sourceDatabase || len(page.Items) != 1 || page.Items[0].ID != strconv.FormatUint(second, 10) ||
		page.Items[0].Liked != 0 || page.Items[0].Comments != 0 || page.Items[0].LikedByMe || page.NextCursor == nil {
		t.Fatalf("cold Feed returned the wrong authorized page: %+v", page)
	}
	if stats := resources.SQL.Stats(); stats.InUse != 0 || stats.WaitCount != waitsBefore {
		t.Fatalf("Feed kept or nested-borrowed the only connection: in-use=%d waits=%d", stats.InUse, stats.WaitCount-waitsBefore)
	}
	// A populated Redis value proves the fix still uses the cold cache loader,
	// rather than bypassing the cache and always returning the row fallback.
	key := "community:counts:" + strconv.FormatUint(second, 10)
	raw, err := resources.Redis.Get(ctx, key).Bytes()
	var cached communitycache.Counts
	if err != nil || json.Unmarshal(raw, &cached) != nil || cached != (communitycache.Counts{}) {
		t.Fatal("cold Feed did not populate its public counter cache")
	}
	if resources.Redis.Set(ctx, key, `{"likes":7,"comments":3}`, time.Minute).Err() != nil {
		t.Fatal("cannot establish owned cache hit")
	}
	hit, err := service.Feed(ctx, followerID, "", "", 1)
	if err != nil || len(hit.Items) != 1 || hit.Items[0].ID != page.Items[0].ID ||
		hit.Items[0].Liked != 7 || hit.Items[0].Comments != 3 || hit.Items[0].LikedByMe {
		t.Fatalf("public cache hit or personal like semantics changed: %+v %v", hit, err)
	}
	next, err := service.Feed(ctx, followerID, page.NextCursor.PublishedAt, page.NextCursor.ID, 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != strconv.FormatUint(first, 10) {
		t.Fatalf("same-time cursor changed after counter assembly: %+v %v", next, err)
	}
	if _, err = service.SetFollow(ctx, followerID, authorID, false); err != nil {
		t.Fatal("owned unfollow failed")
	}
	empty, err := service.Feed(ctx, followerID, "", "", 1)
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("cached counters must not restore an unfollowed page: %+v %v", empty, err)
	}
}
