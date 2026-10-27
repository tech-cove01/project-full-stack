package aichat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Lock serializes turns of one conversation. The lease must outlive the whole
// turn: queue wait, retrieval, generation and streaming.
type Lock struct{ redis *redis.Client }

func NewLock(client *redis.Client) *Lock { return &Lock{redis: client} }

func lockKey(userID uint64, conversationID string) string {
	return "ai:lock:" + strconv.FormatUint(userID, 10) + ":" + conversationID
}

var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// Try returns a token when the lease was acquired, and "" when another turn holds it.
func (lock *Lock) Try(ctx context.Context, userID uint64, conversationID string, ttl time.Duration) (string, error) {
	if lock == nil {
		return "", nil
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(entropy[:])
	acquired, err := lock.redis.SetNX(ctx, lockKey(userID, conversationID), token, ttl).Result()
	if err != nil {
		return "", err
	}
	if !acquired {
		return "", nil
	}
	return token, nil
}

// Owns reports whether this turn still holds the lease. A turn that lost it must
// not stream or persist a result.
func (lock *Lock) Owns(ctx context.Context, userID uint64, conversationID, token string) (bool, error) {
	if lock == nil || token == "" {
		return true, nil
	}
	current, err := lock.redis.Get(ctx, lockKey(userID, conversationID)).Result()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}
	return current == token, nil
}

// Release only deletes its own lease, so it cannot free a newer turn's lock.
func (lock *Lock) Release(ctx context.Context, userID uint64, conversationID, token string) error {
	if lock == nil || token == "" {
		return nil
	}
	_, err := releaseScript.Run(ctx, lock.redis, []string{lockKey(userID, conversationID)}, token).Result()
	return err
}
