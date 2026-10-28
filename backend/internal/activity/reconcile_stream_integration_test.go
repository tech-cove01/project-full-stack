package activity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/redis/go-redis/v9"
)

func TestIntegrationReconcileUnreadStream(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("requires isolated Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("isolated configuration invalid")
	}
	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port), DB: cfg.Redis.Database, Password: cfg.Redis.Password})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	var entropy [12]byte
	_, _ = rand.Read(entropy[:])
	stream := "review-stream:" + hex.EncodeToString(entropy[:])
	t.Cleanup(func() { _ = client.Del(ctx, stream).Err() })
	if state, err := streamInflight(ctx, client, stream); err != nil || state != 0 {
		t.Errorf("missing stream should be idle: state=%d err=%v", state, err)
	}
	if _, err := client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"id": "11"}}).Result(); err != nil {
		t.Fatal(err)
	}
	if state, err := streamInflight(ctx, client, stream); err != nil || state != 2 {
		t.Fatalf("unread messages without a consumer group must block rebuild: state=%d err=%v", state, err)
	}
	if err := client.XGroupCreate(ctx, stream, "review", "0").Err(); err != nil {
		t.Fatal(err)
	}
	rows, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "review", Consumer: "review", Streams: []string{stream, ">"}, Count: 1, Block: -1}).Result()
	if err != nil || len(rows) != 1 || len(rows[0].Messages) != 1 {
		t.Fatal("cannot prepare pending entry")
	}
	if state, err := streamInflight(ctx, client, stream); err != nil || state != 1 {
		t.Fatal("pending message must block rebuild")
	}
	if client.XAck(ctx, stream, "review", rows[0].Messages[0].ID).Err() != nil {
		t.Fatal("cannot ACK owned fixture")
	}
	if state, err := streamInflight(ctx, client, stream); err != nil || state != 0 {
		t.Fatal("fully acknowledged stream must be idle")
	}
}
