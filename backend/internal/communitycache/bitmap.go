package communitycache

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
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

func versionsKey(noteID uint64) string {
	return "community:likes:versions:" + strconv.FormatUint(noteID, 10)
}

// Fact includes inactive rows because their version fences an older delayed like.
type Fact struct {
	UserID  uint64
	Active  int
	Version int
}

var applyLike = redis.NewScript(`
if #KEYS~=2 or KEYS[1]==KEYS[2] or #ARGV~=3 then return redis.error_reply('invalid like projection arguments') end
local user=ARGV[1]
local version=tonumber(ARGV[2])
if not string.match(user,'^[1-9]%d*$') or #user>20 or (#user==20 and user>'18446744073709551615')
  or not string.match(ARGV[2],'^[1-9]%d*$') or not version or version>2147483647
  or (ARGV[3]~='0' and ARGV[3]~='1') then return redis.error_reply('invalid like projection arguments') end
local st=redis.call('TYPE',KEYS[1]).ok
local vt=redis.call('TYPE',KEYS[2]).ok
if (st~='none' and st~='set') or (vt~='none' and vt~='hash') then return redis.error_reply('invalid like projection type') end
local previous=tonumber(redis.call('HGET',KEYS[2],ARGV[1]) or '0')
if not previous or previous<0 or previous>2147483647 or previous~=math.floor(previous) then return redis.error_reply('invalid like projection version') end
if version<=previous then return 0 end
-- A lost set must not inherit the old hash's completeness certificate.
local expected=redis.call('HGET',KEYS[2],'__count')
if expected and tonumber(expected)~=redis.call('SCARD',KEYS[1]) then redis.call('HDEL',KEYS[2],'__complete','__count') end
local changed
if ARGV[3]=='1' then changed=redis.call('SADD',KEYS[1],ARGV[1]) else changed=redis.call('SREM',KEYS[1],ARGV[1]) end
redis.call('HSET',KEYS[2],ARGV[1],ARGV[2])
if redis.call('HGET',KEYS[2],'__complete')=='1' then redis.call('HSET',KEYS[2],'__count',redis.call('SCARD',KEYS[1])) end
return changed
`)

var countLikes = redis.NewScript(`
if #KEYS~=2 or KEYS[1]==KEYS[2] or #ARGV~=0 then return {-1} end
local st=redis.call('TYPE',KEYS[1]).ok
local vt=redis.call('TYPE',KEYS[2]).ok
if (st~='none' and st~='set') or (vt~='none' and vt~='hash') then return {-1} end
if redis.call('HGET',KEYS[2],'__complete')~='1' then return {-1} end
local expected=tonumber(redis.call('HGET',KEYS[2],'__count') or '')
local count=redis.call('SCARD',KEYS[1])
if not expected or expected~=count then return {-1} end
return {count}
`)

var replaceLikes = redis.NewScript(`
if #KEYS~=2 or KEYS[1]==KEYS[2] or #ARGV%3~=0 then return redis.error_reply('invalid like snapshot arguments') end
local seen={}
for i=1,#ARGV,3 do
  local user=ARGV[i]
  local version=tonumber(ARGV[i+1])
  if not string.match(user,'^[1-9]%d*$') or #user>20 or (#user==20 and user>'18446744073709551615') or seen[user]
    or not string.match(ARGV[i+1],'^[1-9]%d*$') or not version or version>2147483647
    or (ARGV[i+2]~='0' and ARGV[i+2]~='1') then return redis.error_reply('invalid like snapshot arguments') end
  seen[user]=true
end
local st=redis.call('TYPE',KEYS[1]).ok
local vt=redis.call('TYPE',KEYS[2]).ok
if (st~='none' and st~='set') or (vt~='none' and vt~='hash') then return redis.error_reply('invalid like projection type') end
redis.call('DEL',KEYS[1],KEYS[2])
for i=1,#ARGV,3 do
  redis.call('HSET',KEYS[2],ARGV[i],ARGV[i+1])
  if ARGV[i+2]=='1' then redis.call('SADD',KEYS[1],ARGV[i]) end
end
local count=redis.call('SCARD',KEYS[1])
redis.call('HSET',KEYS[2],'__complete','1','__count',count)
return count
`)

// Mark adds or removes the like fact of one user. Repeating a state is
// idempotent; the return value reports whether the set actually changed.
func (likes *Likes) Mark(ctx context.Context, noteID, userID uint64, liked bool, version int) (bool, error) {
	if noteID == 0 || userID == 0 || version < 1 || version > 2147483647 {
		return false, errors.New("like projection needs a real user id")
	}
	active := "0"
	if liked {
		active = "1"
	}
	changed, err := applyLike.Run(ctx, likes.redis, []string{likesKey(noteID), versionsKey(noteID)},
		strconv.FormatUint(userID, 10), strconv.Itoa(version), active).Int64()
	return changed > 0, err
}

// Count reads the projection, reporting ok=false when the key does not exist so
// the caller can fall back to MySQL instead of reading zero.
func (likes *Likes) Count(ctx context.Context, noteID uint64) (int64, bool, error) {
	counts, err := countLikes.Run(ctx, likes.redis, []string{likesKey(noteID), versionsKey(noteID)}).Int64Slice()
	if err != nil || len(counts) != 1 {
		return 0, false, err
	}
	if counts[0] < 0 {
		return 0, false, nil
	}
	return counts[0], true, nil
}

// Rebuild replaces the projection from the authoritative rows. The delete and the
// insert are one transaction, so a reader sees either the old or the new set and
// never a half-written one.
func (likes *Likes) Rebuild(ctx context.Context, noteID uint64, facts []Fact) (int64, error) {
	if noteID == 0 {
		return 0, errors.New("invalid note id")
	}
	args := make([]any, 0, len(facts)*3)
	seen := make(map[uint64]bool, len(facts))
	for _, fact := range facts {
		if fact.UserID == 0 || fact.Version < 1 || fact.Version > 2147483647 || (fact.Active != 0 && fact.Active != 1) || seen[fact.UserID] {
			return 0, errors.New("invalid like fact")
		}
		seen[fact.UserID] = true
		args = append(args, strconv.FormatUint(fact.UserID, 10), strconv.Itoa(fact.Version), strconv.Itoa(fact.Active))
	}
	return replaceLikes.Run(ctx, likes.redis, []string{likesKey(noteID), versionsKey(noteID)}, args...).Int64()
}

// RebuildFromDatabase locks the same note row as SetLike. The fact snapshot and
// atomic Redis replacement therefore cannot erase a concurrently committed like.
func (likes *Likes) RebuildFromDatabase(ctx context.Context, db *gorm.DB, noteID uint64) (int64, error) {
	var written int64
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var exists uint64
		if err := tx.Raw("SELECT id FROM tb_blog WHERE id=? FOR UPDATE", noteID).Scan(&exists).Error; err != nil {
			return err
		}
		if exists == 0 {
			return errors.New("note does not exist")
		}
		var facts []Fact
		if err := tx.Raw("SELECT user_id,active,version FROM tb_blog_like WHERE blog_id=? ORDER BY user_id", noteID).Scan(&facts).Error; err != nil {
			return err
		}
		var err error
		written, err = likes.Rebuild(ctx, noteID, facts)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return written, err
}
