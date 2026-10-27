package aichat

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DedupeState is the outcome of claiming a client message id.
type DedupeState int

const (
	DedupeNew DedupeState = iota + 1
	DedupeCached
	DedupeInProgress
	DedupeConflict
)

const (
	processingTTL = time.Hour
	completedTTL  = 10 * time.Minute
)

// Dedupe stores the in-flight or finished answer for one clientMessageId. The
// claim is a single script, so two concurrent retries cannot both become NEW.
type Dedupe struct{ redis *redis.Client }

func NewDedupe(client *redis.Client) *Dedupe { return &Dedupe{redis: client} }

func dedupeKey(userID uint64, conversationID, clientMessageID string) string {
	return "ai:dedupe:" + strconv.FormatUint(userID, 10) + ":" + conversationID + ":" + clientMessageID
}

var dedupeBeginScript = redis.NewScript(`
local key = KEYS[1]
local status = redis.call('HGET', key, 'status')
if not status then
  redis.call('HSET', key, 'status', 'PROCESSING', 'fingerprint', ARGV[1])
  redis.call('EXPIRE', key, ARGV[2])
  return 1
end
if redis.call('HGET', key, 'fingerprint') ~= ARGV[1] then return -1 end
if status == 'COMPLETED' then return 2 end
return 3
`)

// Begin claims the id. It returns the stored answer for a completed id so the
// caller can replay it instead of calling the provider again.
func (dedupe *Dedupe) Begin(ctx context.Context, userID uint64, conversationID, clientMessageID,
	fingerprint string) (DedupeState, string, error) {
	if dedupe == nil || clientMessageID == "" {
		return DedupeNew, "", nil
	}
	key := dedupeKey(userID, conversationID, clientMessageID)
	result, err := dedupeBeginScript.Run(ctx, dedupe.redis, []string{key}, fingerprint,
		strconv.Itoa(int(processingTTL.Seconds()))).Int()
	if err != nil {
		return DedupeNew, "", err
	}
	switch result {
	case -1:
		return DedupeConflict, "", nil
	case 2:
		answer, err := dedupe.redis.HGet(ctx, key, "answer").Result()
		if err != nil {
			// The payload expired; the client must use a new message id rather than
			// receive a hollow "completed".
			return DedupeConflict, "", nil
		}
		return DedupeCached, answer, nil
	case 3:
		return DedupeInProgress, "", nil
	default:
		return DedupeNew, "", nil
	}
}

// Complete keeps the answer briefly so an immediate retry replays it.
func (dedupe *Dedupe) Complete(ctx context.Context, userID uint64, conversationID,
	clientMessageID, answer string) error {
	if dedupe == nil || clientMessageID == "" {
		return nil
	}
	key := dedupeKey(userID, conversationID, clientMessageID)
	pipe := dedupe.redis.TxPipeline()
	pipe.HSet(ctx, key, "status", "COMPLETED", "answer", answer)
	pipe.Expire(ctx, key, completedTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// Fail removes the claim so a failed turn can be retried with the same id.
func (dedupe *Dedupe) Fail(ctx context.Context, userID uint64, conversationID, clientMessageID string) error {
	if dedupe == nil || clientMessageID == "" {
		return nil
	}
	return dedupe.redis.Del(ctx, dedupeKey(userID, conversationID, clientMessageID)).Err()
}
