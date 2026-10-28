package communitycache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// The suite owns one note key and deletes it; it never touches other notes.
func TestIntegrationLikeBitmap(t *testing.T) {
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
	bitmap := NewLikes(resources.Redis)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.Redis.Del(cleanupCtx, likesKey(noteID), versionsKey(noteID)).Err()
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_blog WHERE id=?", noteID).Error
	})

	if count, present, err := bitmap.Count(ctx, noteID); err != nil || present || count != 0 {
		t.Fatalf("an absent bitmap must report present=false, got %d %v %v", count, present, err)
	}
	if _, err = bitmap.Mark(ctx, noteID, 7, true, 1); err != nil {
		t.Fatal(err)
	}
	count, present, err := bitmap.Count(ctx, noteID)
	if err != nil || present || count != 0 {
		t.Fatalf("an incremental-only set has no complete snapshot: %d %v %v", count, present, err)
	}
	if _, err = bitmap.Rebuild(ctx, noteID, []Fact{{UserID: 7, Active: 1, Version: 1}}); err != nil {
		t.Fatal(err)
	}
	// Repeating the state must not add a second bit.
	if _, err = bitmap.Mark(ctx, noteID, 7, true, 1); err != nil {
		t.Fatal(err)
	}
	if count, _, _ = bitmap.Count(ctx, noteID); count != 1 {
		t.Fatalf("a repeated like must stay at one, got %d", count)
	}
	if _, err = bitmap.Mark(ctx, noteID, 7, false, 2); err != nil {
		t.Fatal(err)
	}
	if count, _, _ = bitmap.Count(ctx, noteID); count != 0 {
		t.Fatalf("an unlike must clear the bit, got %d", count)
	}

	// A rebuild replaces the projection with the authoritative rows.
	written, err := bitmap.Rebuild(ctx, noteID, []Fact{{11, 1, 1}, {12, 1, 1}, {13, 1, 1}})
	if err != nil || written != 3 {
		t.Fatalf("rebuild must write three bits: %d %v", written, err)
	}
	if count, _, _ = bitmap.Count(ctx, noteID); count != 3 {
		t.Fatalf("the rebuilt bitmap must hold three bits, got %d", count)
	}
	// A rebuild with fewer rows must drop the old bits.
	if _, err = bitmap.Rebuild(ctx, noteID, []Fact{{11, 1, 2}}); err != nil {
		t.Fatal(err)
	}
	if count, _, _ = bitmap.Count(ctx, noteID); count != 1 {
		t.Fatalf("a rebuild must replace the previous bits, got %d", count)
	}
	// A user id beyond the 32-bit offset limit is skipped instead of failing.
	if changed, err := bitmap.Mark(ctx, noteID, 0, true, 1); err == nil || changed {
		t.Fatalf("a zero user id must be reported: %v %v", changed, err)
	}
	if count, _, _ = bitmap.Count(ctx, noteID); count != 1 {
		t.Fatalf("a skipped id must not change the count, got %d", count)
	}
	if changed, err := bitmap.Mark(ctx, noteID, 11, false, 1); err != nil || changed {
		t.Fatalf("delayed old version must be ignored: %v %v", changed, err)
	}
	if count, _, _ = bitmap.Count(ctx, noteID); count != 1 {
		t.Fatal("old unlike rewrote a newer snapshot")
	}
	if _, err = bitmap.Rebuild(ctx, noteID, []Fact{{11, 0, 3}}); err != nil {
		t.Fatal(err)
	}
	if changed, err := bitmap.Mark(ctx, noteID, 11, true, 2); err != nil || changed {
		t.Fatalf("inactive snapshot must retain a version fence: %v %v", changed, err)
	}
	if count, present, err = bitmap.Count(ctx, noteID); err != nil || !present || count != 0 {
		t.Fatalf("zero snapshot lost completeness: %d %v %v", count, present, err)
	}
}
