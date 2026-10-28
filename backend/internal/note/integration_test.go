package note

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// The suite creates one row and deletes it by id; it never truncates the table.
func TestIntegrationNotePublishAndDetail(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	var entropy [8]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	userID := uint64(binary.BigEndian.Uint64(entropy[:])) | 1
	service := NewService(resources.ORM)
	view, err := service.Create(ctx, strconv.FormatUint(userID, 10), CreateRequest{
		ShopID: "1", Title: "集成测试笔记", Content: "到店体验很好，核销很快。",
		Images: []string{"https://cdn.example.com/a.jpg"},
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_community_outbox WHERE aggregate_type='BLOG' AND aggregate_id=?", view.ID).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_blog WHERE id=?", view.ID).Error
	})
	if view.Status != "PUBLISHED" || view.PublishVersion != 1 || len(view.Images) != 1 {
		t.Fatalf("published note shape is wrong: %+v", view)
	}
	if view.ShopID == nil || *view.ShopID != "1" {
		t.Fatal("the chosen shop must be stored as a string id")
	}

	detail, err := service.Detail(ctx, view.ID)
	if err != nil || detail.ID != view.ID || detail.Title != view.Title || detail.Content != view.Content {
		t.Fatalf("detail must return the same note, got %+v %v", detail, err)
	}

	if _, err = service.Create(ctx, strconv.FormatUint(userID, 10), CreateRequest{
		ShopID: "999999999", Title: "不存在门店", Content: "正文",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown shop must be rejected, got %v", err)
	}
}
