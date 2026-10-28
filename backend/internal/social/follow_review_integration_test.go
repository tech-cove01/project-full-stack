package social

import (
	"context"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func TestIntegrationConcurrentFirstFollow(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	author, reader := insertUser(t, r, "134"), insertUser(t, r, "133")
	userID, authorID := strconv.FormatUint(reader, 10), strconv.FormatUint(author, 10)
	t.Cleanup(func() {
		_ = r.ORM.Exec("DELETE FROM tb_community_outbox WHERE event_id LIKE ?", "FOLLOW:"+userID+":"+authorID+":%").Error
		_ = r.ORM.Exec("DELETE FROM tb_follow WHERE user_id=? AND follow_user_id=?", reader, author).Error
		_ = r.ORM.Exec("DELETE FROM tb_user WHERE id IN (?,?)", reader, author).Error
	})
	service := NewService(r.ORM)
	noop, err := service.SetFollow(ctx, userID, authorID, false)
	if err != nil || noop.Following || noop.Version != 0 {
		t.Fatalf("initial unfollow is an unchanged state, not a new version: %+v %v", noop, err)
	}
	var initialEvents int64
	if err = r.ORM.Raw("SELECT COUNT(*) FROM tb_community_outbox WHERE event_id LIKE ?", "FOLLOW:"+userID+":"+authorID+":%").Scan(&initialEvents).Error; err != nil || initialEvents != 0 {
		t.Fatalf("initial unfollow emitted a change event: %d %v", initialEvents, err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := service.SetFollow(ctx, userID, authorID, true)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("repeated concurrent first follow must succeed: %v", err)
		}
	}
	state, err := service.Relationship(ctx, userID, authorID)
	if err != nil || !state.Following || state.Version != 1 || state.FollowerCount != 1 {
		t.Fatalf("first follows did not converge: %+v %v", state, err)
	}
	var count int64
	if err = r.ORM.Raw("SELECT COUNT(*) FROM tb_community_outbox WHERE event_id LIKE ?", "FOLLOW:"+userID+":"+authorID+":%").Scan(&count).Error; err != nil || count != 1 {
		t.Fatalf("expected one event for repeated desired state: %d %v", count, err)
	}
}
