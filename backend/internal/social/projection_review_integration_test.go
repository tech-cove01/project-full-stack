package social

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/communitycache"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func TestIntegrationLikeProjectionCannotReplaceFacts(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	author, first, second := insertUser(t, r, "135"), insertUser(t, r, "136"), insertUser(t, r, "137")
	note := insertNote(t, r, author, "投影不能覆盖事实", time.Now().Truncate(time.Second))
	noteID := strconv.FormatUint(note, 10)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_community_outbox WHERE event_id LIKE ?", "LIKE:"+noteID+":%").Error
		_ = r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_blog_like WHERE blog_id=?", note).Error
		_ = r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_blog WHERE id=?", note).Error
		_ = r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_user WHERE id IN (?,?,?)", author, first, second).Error
		_ = r.Redis.Del(cleanup, "community:likes:"+noteID, "community:likes:versions:"+noteID).Err()
	})
	service := NewService(r.ORM).WithLikes(communitycache.NewLikes(r.Redis))
	if _, err = service.SetLike(ctx, strconv.FormatUint(first, 10), noteID, true); err != nil {
		t.Fatal(err)
	}
	if err = r.Redis.Del(ctx, "community:likes:"+noteID, "community:likes:versions:"+noteID).Err(); err != nil {
		t.Fatal(err)
	}
	state, err := service.SetLike(ctx, strconv.FormatUint(second, 10), noteID, true)
	if err != nil {
		t.Fatal(err)
	}
	if state.Likes != 2 {
		t.Fatalf("partial projection replaced two committed facts with count %d", state.Likes)
	}
}
