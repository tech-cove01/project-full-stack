package note

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"gorm.io/gorm"
)

func TestIntegrationPublicationRequiresDurableEvent(t *testing.T) {
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
	userID := strconv.FormatUint(uint64(time.Now().UnixNano()), 10)
	t.Cleanup(func() {
		_ = r.ORM.Exec("DELETE FROM tb_community_outbox WHERE aggregate_type='BLOG' AND aggregate_id IN (SELECT CAST(id AS CHAR) FROM tb_blog WHERE user_id=?)", userID).Error
		_ = r.ORM.Exec("DELETE FROM tb_blog WHERE user_id=?", userID).Error
	})
	service := NewService(r.ORM)
	view, err := service.Create(ctx, userID, CreateRequest{Title: "发布事件", Content: "发布与事件同事务"})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err = r.ORM.Raw("SELECT COUNT(*) FROM tb_community_outbox WHERE aggregate_type='BLOG' AND aggregate_id=? AND event_type='NOTE_PUBLISHED' AND aggregate_version=1", view.ID).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("published note must commit exactly one publication event, got %d", count)
	}
	const callback = "review:reject-note-outbox"
	if err = r.ORM.Callback().Raw().Before("gorm:raw").Register(callback, func(tx *gorm.DB) {
		if strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "insert into tb_community_outbox") {
			tx.AddError(errors.New("controlled publication event failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.ORM.Callback().Raw().Remove(callback) })
	_, err = service.Create(ctx, userID, CreateRequest{Title: "事务必须回滚", Content: "事件落库失败不能留下公开笔记"})
	if err == nil {
		t.Error("publication accepted without a durable outbox event")
	}
	if err = r.ORM.Raw("SELECT COUNT(*) FROM tb_blog WHERE user_id=? AND title=?", userID, "事务必须回滚").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("failed event must roll back the note, got %d committed rows", count)
	}
}
