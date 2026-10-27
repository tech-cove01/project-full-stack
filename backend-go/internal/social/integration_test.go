package social

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// The suite owns two random users, three notes and their follow rows; it deletes
// exactly those ids and never truncates a table or resets a sequence.
func TestIntegrationFollowAndFeed(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	follower := insertUser(t, resources, "139")
	author := insertUser(t, resources, "137")
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.ORM.WithContext(cleanupCtx).Exec(
			"DELETE FROM tb_community_outbox WHERE aggregate_id=?", strconv.FormatUint(author, 10)).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec(
			"DELETE FROM tb_follow WHERE user_id IN (?,?)", follower, author).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec(
			"DELETE FROM tb_blog WHERE user_id=?", author).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec(
			"DELETE FROM tb_user WHERE id IN (?,?)", follower, author).Error
	})

	sameSecond := time.Now().Truncate(time.Second)
	older := sameSecond.Add(-time.Hour)
	first := insertNote(t, resources, author, "第一页笔记", sameSecond)
	second := insertNote(t, resources, author, "同秒第二篇", sameSecond)
	third := insertNote(t, resources, author, "更早的笔记", older)
	_ = first
	_ = second

	service := NewService(resources.ORM)
	userID := strconv.FormatUint(follower, 10)
	authorID := strconv.FormatUint(author, 10)

	relationship, err := service.SetFollow(ctx, userID, authorID, true)
	if err != nil || !relationship.Following || relationship.Version != 1 {
		t.Fatalf("follow failed: %+v %v", relationship, err)
	}
	// Repeating the same state must not bump the version.
	repeated, err := service.SetFollow(ctx, userID, authorID, true)
	if err != nil || repeated.Version != 1 {
		t.Fatalf("a repeated follow must stay at version 1, got %+v %v", repeated, err)
	}

	page, err := service.Feed(ctx, userID, "", "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("first page shape is wrong: %+v %v", page, err)
	}
	seen := map[string]bool{}
	for _, item := range page.Items {
		seen[item.ID] = true
	}
	next, err := service.Feed(ctx, userID, page.NextCursor.PublishedAt, page.NextCursor.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].ID != strconv.FormatUint(third, 10) {
		t.Fatalf("the cursor must return the remaining older note, got %+v", next.Items)
	}
	if seen[next.Items[0].ID] {
		t.Fatal("a later page must not repeat a note from the first page")
	}

	if _, err = service.SetFollow(ctx, userID, authorID, false); err != nil {
		t.Fatal("unfollow failed")
	}
	empty, err := service.Feed(ctx, userID, "", "", 10)
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("after unfollow the feed must be empty, got %+v %v", empty, err)
	}

	var events int64
	if err = resources.ORM.WithContext(ctx).Raw(`SELECT COUNT(*) FROM tb_community_outbox
		WHERE aggregate_id=? AND event_type='FOLLOW_CHANGED'`, authorID).Scan(&events).Error; err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("one event per real change expected, got %d", events)
	}
}

func insertUser(t *testing.T, resources *database.Resources, prefix string) uint64 {
	t.Helper()
	var entropy [4]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	phone := prefix + strconv.FormatUint(uint64(binary.BigEndian.Uint32(entropy[:]))%100000000, 10)
	for len(phone) < 11 {
		phone += "0"
	}
	phone = phone[:11]
	if err := resources.ORM.Exec(`INSERT INTO tb_user (phone,password,nick_name,icon)
		VALUES (?,?,?,?)`, phone, "integration", "集成测试", "").Error; err != nil {
		t.Fatalf("user insert failed: %v", err)
	}
	var id uint64
	if err := resources.ORM.Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error; err != nil || id == 0 {
		t.Fatal("user id unavailable")
	}
	return id
}

func insertNote(t *testing.T, resources *database.Resources, author uint64, title string,
	createdAt time.Time) uint64 {
	t.Helper()
	if err := resources.ORM.Exec(`INSERT INTO tb_blog
		(shop_id,user_id,title,images,content,status,publish_version,create_time,update_time)
		VALUES (0,?,?,?,?,'PUBLISHED',1,?,?)`, author, title, "", "集成测试正文",
		createdAt, createdAt).Error; err != nil {
		t.Fatalf("note insert failed: %v", err)
	}
	var id uint64
	if err := resources.ORM.Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error; err != nil || id == 0 {
		t.Fatal("note id unavailable")
	}
	return id
}
