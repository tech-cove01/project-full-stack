package ordercomp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/redis/go-redis/v9"
)

func TestIntegrationRestoreValidation(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("requires isolated Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated configuration")
	}
	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port), DB: cfg.Redis.Database, Password: cfg.Redis.Password})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	for _, test := range []struct {
		name, stock, max, release string
		args                      int
	}{
		{"fractional_stock", "1.5", "10", "1", 4},
		{"negative_stock", "-1", "10", "1", 4},
		{"noncanonical_stock", "01", "10", "1", 4},
		{"missing_max", "1", "", "1", 3},
		{"invalid_max", "1", "no", "1", 4},
		{"fractional_max", "1", "2.5", "1", 4},
		{"invalid_release", "1", "10", "true", 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			var entropy [12]byte
			if _, err := rand.Read(entropy[:]); err != nil {
				t.Fatal(err)
			}
			prefix := "review-restore:" + hex.EncodeToString(entropy[:])
			keys := []string{prefix + ":done", prefix + ":stock", prefix + ":purchased", prefix + ":accepted"}
			t.Cleanup(func() { _ = client.Del(ctx, keys...).Err() })
			if client.Set(ctx, keys[1], test.stock, 0).Err() != nil || client.SAdd(ctx, keys[2], "11").Err() != nil || client.HSet(ctx, keys[3], "11", "21").Err() != nil {
				t.Fatal("cannot seed keys")
			}
			before := restoreDump(t, client, keys)
			args := []any{"11", "21", test.release, test.max}
			code, err := restore.Run(ctx, client, keys, args[:test.args]...).Int()
			if err != nil || code >= 0 || !reflect.DeepEqual(before, restoreDump(t, client, keys)) {
				t.Fatalf("invalid input mutated business keys or was accepted: code=%d err=%v", code, err)
			}
		})
	}
	t.Run("old_event_does_not_release_new_acceptance", func(t *testing.T) {
		var entropy [12]byte
		_, _ = rand.Read(entropy[:])
		prefix := "review-restore:" + hex.EncodeToString(entropy[:])
		keys := []string{prefix + ":done", prefix + ":stock", prefix + ":purchased", prefix + ":accepted"}
		t.Cleanup(func() { _ = client.Del(ctx, keys...).Err() })
		_ = client.Set(ctx, keys[1], "0", 0).Err()
		_ = client.SAdd(ctx, keys[2], "11").Err()
		_ = client.HSet(ctx, keys[3], "11", "22").Err()
		code, err := restore.Run(ctx, client, keys, "11", "21", "1", "2").Int()
		if err != nil || code != applied || !client.SIsMember(ctx, keys[2], "11").Val() || client.HGet(ctx, keys[3], "11").Val() != "22" {
			t.Fatal("old compensation erased newer purchase ownership")
		}
		if client.Get(ctx, keys[1]).Val() != "1" {
			t.Fatal("old event still restores its own reserved inventory once")
		}
		if code, e := restore.Run(ctx, client, keys, "11", "21", "1", "2").Int(); e != nil || code != duplicate || client.Get(ctx, keys[1]).Val() != "1" {
			t.Fatal("redelivery restored stock twice")
		}
	})
}

func restoreDump(t *testing.T, c *redis.Client, keys []string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make([]string, len(keys))
	for i, key := range keys {
		value, err := c.Dump(ctx, key).Result()
		if err != nil && err != redis.Nil {
			t.Fatal("cannot inspect key")
		}
		result[i] = value
	}
	return result
}
