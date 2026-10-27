package aichat

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

// The suite owns exact random keys and deletes them; it never flushes Redis or
// touches another user's conversation.
func TestIntegrationDedupeAndLock(t *testing.T) {
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
	conversation := "it-conv"
	message := "it-message"
	dedupe := NewDedupe(resources.Redis)
	lock := NewLock(resources.Redis)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.Redis.Del(cleanupCtx,
			dedupeKey(userID, conversation, message), lockKey(userID, conversation)).Err()
	})

	state, _, err := dedupe.Begin(ctx, userID, conversation, message, "fingerprint-a")
	if err != nil || state != DedupeNew {
		t.Fatalf("first claim must be NEW, got %v %v", state, err)
	}
	if state, _, _ = dedupe.Begin(ctx, userID, conversation, message, "fingerprint-a"); state != DedupeInProgress {
		t.Fatalf("a concurrent claim must be IN_PROGRESS, got %v", state)
	}
	if state, _, _ = dedupe.Begin(ctx, userID, conversation, message, "fingerprint-b"); state != DedupeConflict {
		t.Fatalf("the same id with other content must CONFLICT, got %v", state)
	}
	payload := `{"text":"缓存答复","citationIds":["chunk:1"]}`
	if err = dedupe.Complete(ctx, userID, conversation, message, payload); err != nil {
		t.Fatal("complete failed")
	}
	state, cached, err := dedupe.Begin(ctx, userID, conversation, message, "fingerprint-a")
	if err != nil || state != DedupeCached || cached != payload {
		t.Fatalf("a completed id must replay its answer, got %v %q %v", state, cached, err)
	}
	if err = dedupe.Fail(ctx, userID, conversation, message); err != nil {
		t.Fatal("fail cleanup failed")
	}
	if state, _, _ = dedupe.Begin(ctx, userID, conversation, message, "fingerprint-a"); state != DedupeNew {
		t.Fatalf("after cleanup the id must be claimable again, got %v", state)
	}

	budget := NewBudgetWithLimits(resources.Redis, 2, 2)
	rateKey := "ai:rate:" + strconv.FormatUint(userID, 10) + ":" + time.Now().Format("200601021504")
	dailyKey := "ai:budget:" + strconv.FormatUint(userID, 10) + ":" +
		time.Now().In(zoneForBudget()).Format("20060102")
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.Redis.Del(cleanupCtx, rateKey, dailyKey).Err()
	})
	if err = budget.Allow(ctx, userID); err != nil {
		t.Fatal("first request must pass the minute limit")
	}
	if err = budget.Allow(ctx, userID); err != nil {
		t.Fatal("second request must pass the minute limit")
	}
	if err = budget.Allow(ctx, userID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("third request must hit the minute limit, got %v", err)
	}
	// A fresh minute with the daily cap already used must report the daily error.
	if err = resources.Redis.Del(ctx, rateKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err = budget.Allow(ctx, userID); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("the daily cap must be reported separately, got %v", err)
	}

	token, err := lock.Try(ctx, userID, conversation, time.Minute)
	if err != nil || token == "" {
		t.Fatal("first lease must be acquired")
	}
	if second, _ := lock.Try(ctx, userID, conversation, time.Minute); second != "" {
		t.Fatal("a held lease must not be acquired twice")
	}
	if owns, _ := lock.Owns(ctx, userID, conversation, token); !owns {
		t.Fatal("the holder must own the lease")
	}
	if owns, _ := lock.Owns(ctx, userID, conversation, "other-token"); owns {
		t.Fatal("a foreign token must not own the lease")
	}
	if err = lock.Release(ctx, userID, conversation, token); err != nil {
		t.Fatal("release failed")
	}
	if owns, _ := lock.Owns(ctx, userID, conversation, token); owns {
		t.Fatal("a released lease must not be owned anymore")
	}
}
