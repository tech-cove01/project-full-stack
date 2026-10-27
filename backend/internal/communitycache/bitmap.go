package communitycache

import (
	"context"
	"errors"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// Likes stores the active like facts of a note as a Redis set of user ids.
//
// The platform user id is a snowflake, so it cannot address a single bit in a
// bitmap: an offset beyond 32 bits either fails or has to be folded, which would
// count a user twice. The set keeps the count exact for every user id. A bitmap
// keyed by the tb_blog_like row id is the cheaper follow-up once the projection
// is fed from the fact table instead of the user id.
type Likes struct{ redis *redis.Client }

func NewLikes(client *redis.Client) *Likes { return &Likes{redis: client} }

func likesKey(noteID uint64) string {
	return "community:likes:" + strconv.FormatUint(noteID, 10)
}

// Mark adds or removes the like fact of one user. Repeating a state is
// idempotent; the return value reports whether the set actually changed.
func (likes *Likes) Mark(ctx context.Context, noteID, userID uint64, liked bool) (bool, error) {
	key := likesKey(noteID)
	if userID == 0 {
		return false, errors.New("like projection needs a real user id")
	}
	if liked {
		added, err := likes.redis.SAdd(ctx, key, userID).Result()
		return added > 0, err
	}
	removed, err := likes.redis.SRem(ctx, key, userID).Result()
	return removed > 0, err
}

// Count reads the projection, reporting ok=false when the key does not exist so
// the caller can fall back to MySQL instead of reading zero.
func (likes *Likes) Count(ctx context.Context, noteID uint64) (int64, bool, error) {
	key := likesKey(noteID)
	exists, err := likes.redis.Exists(ctx, key).Result()
	if err != nil || exists == 0 {
		return 0, false, err
	}
	count, err := likes.redis.SCard(ctx, key).Result()
	if err != nil {
		return 0, false, err
	}
	return count, true, nil
}

// Rebuild replaces the projection from the authoritative rows. The delete and the
// insert are one transaction, so a reader sees either the old or the new set and
// never a half-written one.
func (likes *Likes) Rebuild(ctx context.Context, noteID uint64, userIDs []uint64) (int64, error) {
	key := likesKey(noteID)
	pipe := likes.redis.TxPipeline()
	pipe.Del(ctx, key)
	members := make([]any, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID == 0 {
			continue
		}
		members = append(members, userID)
	}
	if len(members) > 0 {
		pipe.SAdd(ctx, key, members...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return int64(len(members)), nil
}
