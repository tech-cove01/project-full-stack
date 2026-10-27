package communitycache

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// Bitmap stores one bit per user id for a note. The platform user id is a
// monotonically increasing integer, so the bit offset is the id itself; a value
// beyond the 32-bit offset limit is skipped and the database counter stays the
// fallback for that note.
type Bitmap struct{ redis *redis.Client }

func NewBitmap(client *redis.Client) *Bitmap { return &Bitmap{redis: client} }

func bitmapKey(noteID uint64) string {
	return "community:likes:" + strconv.FormatUint(noteID, 10)
}

func offset(userID uint64) (int64, bool) {
	if userID == 0 || userID > math.MaxUint32 {
		return 0, false
	}
	return int64(userID), true
}

// Mark sets or clears one bit and keeps the key's TTL refreshed. Repeating a state
// is idempotent; the return value reports whether the bit actually changed.
func (bitmap *Bitmap) Mark(ctx context.Context, noteID, userID uint64, liked bool) (bool, error) {
	off, ok := offset(userID)
	if !ok {
		// Skipping silently would hide a broken projection, so the caller can log it.
		return false, errors.New("like bitmap offset is outside the supported range")
	}
	value := 0
	if liked {
		value = 1
	}
	// No TTL: the bitmap is the counter projection and is rebuilt from MySQL by the
	// recount command, not expired on a timer.
	changed, err := bitmap.redis.SetBit(ctx, bitmapKey(noteID), off, value).Result()
	if err != nil {
		return false, err
	}
	return changed != int64(value), nil
}

// Count reads the bitmap, reporting ok=false when the key does not exist yet so
// the caller can fall back to MySQL instead of reading zero.
func (bitmap *Bitmap) Count(ctx context.Context, noteID uint64) (int64, bool, error) {
	key := bitmapKey(noteID)
	exists, err := bitmap.redis.Exists(ctx, key).Result()
	if err != nil || exists == 0 {
		return 0, false, err
	}
	count, err := bitmap.redis.BitCount(ctx, key, &redis.BitCount{Start: 0, End: -1}).Result()
	if err != nil {
		return 0, false, err
	}
	return count, true, nil
}

// Rebuild replaces the bitmap from the authoritative rows. The delete and the
// batch of set bits are one pipeline, so a reader sees either the old or the new
// projection, never a half-written one.
func (bitmap *Bitmap) Rebuild(ctx context.Context, noteID uint64, userIDs []uint64) (int64, error) {
	key := bitmapKey(noteID)
	pipe := bitmap.redis.TxPipeline()
	pipe.Del(ctx, key)
	written := 0
	for _, userID := range userIDs {
		off, ok := offset(userID)
		if !ok {
			continue
		}
		pipe.SetBit(ctx, key, off, 1)
		written++
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return int64(written), nil
}
