// Package communitycache caches the public counters of a note. MySQL stays the
// fact source: a miss reads the row, a hit serves a short-lived copy, and a
// mutation deletes the key instead of trying to write a new value.
package communitycache

import (
	"context"
	"encoding/json"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	baseTTL       = 60 * time.Second
	jitterPercent = 15
)

type Counts struct {
	Likes    int64 `json:"likes"`
	Comments int64 `json:"comments"`
}

type Cache struct {
	redis *redis.Client
	db    *gorm.DB

	mu       sync.Mutex
	inflight map[uint64]*flight
}

type flight struct {
	wg    sync.WaitGroup
	value Counts
	err   error
}

func New(client *redis.Client, db *gorm.DB) *Cache {
	return &Cache{redis: client, db: db, inflight: make(map[uint64]*flight)}
}

func key(noteID uint64) string { return "community:counts:" + strconv.FormatUint(noteID, 10) }

// jitter keeps a page of keys from expiring in the same second.
func jitter() time.Duration {
	spread := int64(baseTTL) * jitterPercent / 100
	return time.Duration(rand.Int63n(spread))
}

func (cache *Cache) read(ctx context.Context, noteID uint64) (Counts, bool) {
	raw, err := cache.redis.Get(ctx, key(noteID)).Result()
	if err != nil {
		return Counts{}, false
	}
	var value Counts
	if json.Unmarshal([]byte(raw), &value) != nil {
		return Counts{}, false
	}
	return value, true
}

func (cache *Cache) write(ctx context.Context, noteID uint64, value Counts) {
	body, err := json.Marshal(value)
	if err != nil {
		return
	}
	// A cache write failure must never fail the request.
	_ = cache.redis.Set(ctx, key(noteID), string(body), baseTTL+jitter()).Err()
}

func (cache *Cache) loadOne(ctx context.Context, noteID uint64) (Counts, error) {
	var row struct {
		Liked    int64
		Comments int64
	}
	err := cache.db.WithContext(ctx).Raw(`SELECT liked,comments FROM tb_blog
		WHERE id=? AND status='PUBLISHED'`, noteID).Scan(&row).Error
	if err != nil {
		return Counts{}, err
	}
	return Counts{Likes: row.Liked, Comments: row.Comments}, nil
}

// Get serves one note with a singleflight so a burst on the same key performs a
// single database read. A miss caused by a concurrent delete simply reloads.
func (cache *Cache) Get(ctx context.Context, noteID uint64) (Counts, error) {
	if value, ok := cache.read(ctx, noteID); ok {
		return value, nil
	}
	cache.mu.Lock()
	if existing, ok := cache.inflight[noteID]; ok {
		cache.mu.Unlock()
		existing.wg.Wait()
		return existing.value, existing.err
	}
	entry := &flight{}
	entry.wg.Add(1)
	cache.inflight[noteID] = entry
	cache.mu.Unlock()
	defer func() {
		cache.mu.Lock()
		delete(cache.inflight, noteID)
		cache.mu.Unlock()
		entry.wg.Done()
	}()
	// Another goroutine may have filled the cache between the first read and
	// winning the flight; prefer the cached value over a second query.
	if value, ok := cache.read(ctx, noteID); ok {
		entry.value = value
		return value, nil
	}
	value, err := cache.loadOne(ctx, noteID)
	if err != nil {
		entry.err = err
		return Counts{}, err
	}
	cache.write(ctx, noteID, value)
	entry.value = value
	return value, nil
}

// GetMany reads the whole page in one MGET and one IN query for the misses. It is
// the feed path: batching already removes the N+1, so it does not also need
// per-key flights.
func (cache *Cache) GetMany(ctx context.Context, noteIDs []uint64) (map[uint64]Counts, error) {
	result := make(map[uint64]Counts, len(noteIDs))
	if len(noteIDs) == 0 {
		return result, nil
	}
	keys := make([]string, 0, len(noteIDs))
	for _, id := range noteIDs {
		keys = append(keys, key(id))
	}
	values, err := cache.redis.MGet(ctx, keys...).Result()
	if err != nil {
		// Degrade to the database rather than fail the feed.
		values = make([]any, len(noteIDs))
	}
	missing := make([]uint64, 0, len(noteIDs))
	for index, raw := range values {
		if text, ok := raw.(string); ok {
			var value Counts
			if json.Unmarshal([]byte(text), &value) == nil {
				result[noteIDs[index]] = value
				continue
			}
		}
		missing = append(missing, noteIDs[index])
	}
	if len(missing) == 0 {
		return result, nil
	}
	var rows []struct {
		ID       uint64
		Liked    int64
		Comments int64
	}
	placeholders := ""
	args := make([]any, 0, len(missing))
	for index, id := range missing {
		if index > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id)
	}
	if err = cache.db.WithContext(ctx).Raw(`SELECT id,liked,comments FROM tb_blog
		WHERE status='PUBLISHED' AND id IN (`+placeholders+`)`, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		value := Counts{Likes: row.Liked, Comments: row.Comments}
		result[row.ID] = value
		cache.write(ctx, row.ID, value)
	}
	return result, nil
}

// Invalidate deletes the key after a mutation so the next read rebuilds it.
func (cache *Cache) Invalidate(ctx context.Context, noteID uint64) error {
	return cache.redis.Del(ctx, key(noteID)).Err()
}
