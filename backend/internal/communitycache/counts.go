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

	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
)

const (
	baseTTL       = 60 * time.Second
	jitterPercent = 15
	// A short in-process copy absorbs a hot key: a popular note is served without
	// touching Redis on every request, while the value is never older than this.
	defaultLocalTTL = 2 * time.Second
	// Fixed stripes bound the ordering state. Different stripes do not hold one
	// another across Redis I/O; colliding note IDs intentionally share ordering.
	countPublicationShards = 4096
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
	// Same-note publication and invalidation share an ordering boundary. A slow
	// Redis write/delete no longer takes a network lock over the whole cache.
	publication [countPublicationShards]countPublicationShard

	localMu  sync.Mutex
	local    map[uint64]localEntry
	localTTL time.Duration
}

type countPublicationShard struct {
	once       sync.Once
	gate       chan struct{}
	generation uint64
}

type flight struct {
	done       chan struct{}
	generation uint64
	value      Counts
	err        error
}

func (cache *Cache) acquirePublication(ctx context.Context, noteID uint64) (*countPublicationShard, error) {
	shard := &cache.publication[noteID%countPublicationShards]
	shard.once.Do(func() { shard.gate = make(chan struct{}, 1) })
	span := taildiag.Begin(ctx, taildiag.PublicationWait, 0)
	select {
	case shard.gate <- struct{}{}:
		span.End(false)
		return shard, nil
	case <-ctx.Done():
		span.End(true)
		return nil, ctx.Err()
	}
}

func (cache *Cache) epoch(ctx context.Context, noteID uint64) (uint64, error) {
	shard, err := cache.acquirePublication(ctx, noteID)
	if err != nil {
		return 0, err
	}
	hold := taildiag.Begin(ctx, taildiag.PublicationHold, 0)
	defer func() { hold.End(false); <-shard.gate }()
	return shard.generation, nil
}

func (cache *Cache) publish(ctx context.Context, noteID uint64, value Counts, epoch uint64, toRedis bool) {
	shard, err := cache.acquirePublication(ctx, noteID)
	if err != nil {
		return
	}
	hold := taildiag.Begin(ctx, taildiag.PublicationHold, 0)
	defer func() { hold.End(false); <-shard.gate }()
	if shard.generation != epoch {
		return
	}
	if toRedis {
		cache.write(ctx, noteID, value)
	}
	cache.writeLocal(noteID, value)
}

type localEntry struct {
	value  Counts
	expire time.Time
}

func New(client *redis.Client, db *gorm.DB) *Cache {
	return &Cache{redis: client, db: db, inflight: make(map[uint64]*flight),
		local: make(map[uint64]localEntry), localTTL: defaultLocalTTL}
}

// WithLocalTTL overrides the in-process lifetime; tests use a short value so the
// expiry path is exercised deterministically.
func (cache *Cache) WithLocalTTL(ttl time.Duration) *Cache {
	if ttl < 0 {
		ttl = 0
	}
	cache.localMu.Lock()
	cache.localTTL = ttl
	cache.localMu.Unlock()
	return cache
}

func (cache *Cache) readLocal(noteID uint64) (Counts, bool) {
	cache.localMu.Lock()
	defer cache.localMu.Unlock()
	entry, ok := cache.local[noteID]
	if !ok || time.Now().After(entry.expire) {
		if ok {
			delete(cache.local, noteID)
		}
		return Counts{}, false
	}
	return entry.value, true
}

func (cache *Cache) writeLocal(noteID uint64, value Counts) {
	cache.localMu.Lock()
	defer cache.localMu.Unlock()
	if cache.localTTL <= 0 {
		return
	}
	// Bounded memory: a hot key set is small, but a scrape of many notes must not
	// grow the map forever.
	if len(cache.local) >= 4096 {
		clear(cache.local)
	}
	cache.local[noteID] = localEntry{value: value, expire: time.Now().Add(cache.localTTL)}
}

func (cache *Cache) dropLocal(noteID uint64) {
	cache.localMu.Lock()
	defer cache.localMu.Unlock()
	delete(cache.local, noteID)
}

func key(noteID uint64) string { return "community:counts:" + strconv.FormatUint(noteID, 10) }

// jitter keeps a page of keys from expiring in the same second.
func jitter() time.Duration {
	spread := int64(baseTTL) * jitterPercent / 100
	return time.Duration(rand.Int63n(spread))
}

func (cache *Cache) read(ctx context.Context, noteID uint64) (Counts, bool) {
	span := taildiag.Begin(ctx, taildiag.CountsRedisRead, 1)
	raw, err := cache.redis.Get(ctx, key(noteID)).Result()
	span.End(err != nil)
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
	span := taildiag.Begin(ctx, taildiag.CountsRedisWrite, 1)
	err = cache.redis.Set(ctx, key(noteID), string(body), baseTTL+jitter()).Err()
	span.End(err != nil)
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
	if err := ctx.Err(); err != nil {
		return Counts{}, err
	}
	epoch, err := cache.epoch(ctx, noteID)
	if err != nil {
		return Counts{}, err
	}
	if value, ok := cache.readLocal(noteID); ok {
		return value, nil
	}
	if value, ok := cache.read(ctx, noteID); ok {
		cache.publish(ctx, noteID, value, epoch, false)
		return value, nil
	}
	cache.mu.Lock()
	if existing, ok := cache.inflight[noteID]; ok && existing.generation == epoch {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return Counts{}, ctx.Err()
		case <-existing.done:
			return existing.value, existing.err
		}
	}
	entry := &flight{done: make(chan struct{}), generation: epoch}
	cache.inflight[noteID] = entry
	cache.mu.Unlock()
	defer func() {
		cache.mu.Lock()
		if cache.inflight[noteID] == entry {
			delete(cache.inflight, noteID)
		}
		cache.mu.Unlock()
		close(entry.done)
	}()
	// Another goroutine may have filled the cache between the first read and
	// winning the flight; prefer the cached value over a second query.
	if value, ok := cache.read(ctx, noteID); ok {
		cache.publish(ctx, noteID, value, epoch, false)
		entry.value = value
		return value, nil
	}
	value, err := cache.loadOne(ctx, noteID)
	if err != nil {
		entry.err = err
		return Counts{}, err
	}
	cache.publish(ctx, noteID, value, epoch, true)
	entry.value = value
	return value, nil
}

// GetMany reads the whole page in one MGET and one IN query for the misses. It is
// the feed path: batching already removes the N+1, so it does not also need
// per-key flights.
func (cache *Cache) GetMany(ctx context.Context, noteIDs []uint64) (out map[uint64]Counts, returnedErr error) {
	span := taildiag.Begin(ctx, taildiag.CountsMany, len(noteIDs))
	defer func() { span.End(returnedErr != nil) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make(map[uint64]Counts, len(noteIDs))
	if len(noteIDs) == 0 {
		return result, nil
	}
	// Capture each stripe epoch without holding multiple gates. The batch keeps
	// one MGET/IN query; it never acquires several network locks in a lock order.
	epochs := make(map[uint64]uint64, len(noteIDs))
	for _, id := range noteIDs {
		epoch, err := cache.epoch(ctx, id)
		if err != nil {
			return nil, err
		}
		epochs[id] = epoch
	}
	// A hot page is served from the in-process copy; only the rest reaches Redis.
	remaining := make([]uint64, 0, len(noteIDs))
	for _, id := range noteIDs {
		if value, ok := cache.readLocal(id); ok {
			result[id] = value
			continue
		}
		remaining = append(remaining, id)
	}
	if len(remaining) == 0 {
		return result, nil
	}
	keys := make([]string, 0, len(remaining))
	for _, id := range remaining {
		keys = append(keys, key(id))
	}
	readSpan := taildiag.Begin(ctx, taildiag.CountsRedisRead, len(keys))
	values, err := cache.redis.MGet(ctx, keys...).Result()
	readSpan.End(err != nil)
	if err != nil {
		// Degrade to the database rather than fail the feed.
		values = make([]any, len(remaining))
	}
	missing := make([]uint64, 0, len(remaining))
	for index, raw := range values {
		if text, ok := raw.(string); ok {
			var value Counts
			if json.Unmarshal([]byte(text), &value) == nil {
				result[remaining[index]] = value
				cache.publish(ctx, remaining[index], value, epochs[remaining[index]], false)
				continue
			}
		}
		missing = append(missing, remaining[index])
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
	coldSpan := taildiag.Begin(ctx, taildiag.CountsColdSQL, len(missing))
	err = cache.db.WithContext(ctx).Raw(`SELECT id,liked,comments FROM tb_blog
		WHERE status='PUBLISHED' AND id IN (`+placeholders+`)`, args...).Scan(&rows).Error
	coldSpan.End(err != nil)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		value := Counts{Likes: row.Liked, Comments: row.Comments}
		result[row.ID] = value
		cache.publish(ctx, row.ID, value, epochs[row.ID], true)
	}
	return result, nil
}

// Invalidate drops both layers after a mutation so the next read rebuilds them.
func (cache *Cache) Invalidate(ctx context.Context, noteID uint64) error {
	shard, err := cache.acquirePublication(ctx, noteID)
	if err != nil {
		return err
	}
	hold := taildiag.Begin(ctx, taildiag.PublicationHold, 0)
	defer func() { hold.End(false); <-shard.gate }()
	shard.generation++
	cache.dropLocal(noteID)
	span := taildiag.Begin(ctx, taildiag.CountsRedisDelete, 1)
	err = cache.redis.Del(ctx, key(noteID)).Err()
	span.End(err != nil)
	return err
}
