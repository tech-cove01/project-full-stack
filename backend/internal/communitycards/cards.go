// Package communitycards caches only immutable, published-version note content.
// Facts, page order, personal state and public counters stay outside this cache.
package communitycards

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrInput          = errors.New("invalid card batch")
	ErrVersionChanged = errors.New("published card version unavailable")
	ErrBusy           = errors.New("card load capacity unavailable")
	ErrClosed         = errors.New("card cache closed")
)

type Ref struct{ ID, Version uint64 }
type Card struct {
	Title, Content string
	Images         []string
}

type limits struct {
	batch, localEntries, maxCardBytes, inflight, active, waiters int
	localBytes                                                   int64
	localTTL, loadTimeout, redisTimeout                          time.Duration
	now                                                          func() time.Time
}

func defaults() limits {
	return limits{batch: 50, localEntries: 2048, localBytes: 8 << 20, maxCardBytes: 8 << 10,
		inflight: 64, active: 8, waiters: 1024, localTTL: 2 * time.Second,
		loadTimeout: 2 * time.Second, redisTimeout: 250 * time.Millisecond, now: time.Now}
}

type counters struct {
	requests, localHits, redisHits, missing, loads, sqlBatches, sqlRows, contentBytes atomic.Uint64
	loadErrors, oversize, redisErrors, invalid, busy, expired, evictions              atomic.Uint64
	flightStarted, flightJoins                                                        atomic.Uint64
}

// Snapshot contains fixed counters only. Bytes are content/payload budgets, not
// SQL wire bytes or a process-memory limit; gauges are concurrent observations.
type Snapshot struct {
	Coalescing        bool   `json:"coalescing"`
	Requests          uint64 `json:"requests"`
	L1Hits            uint64 `json:"l1_hits"`
	RedisHits         uint64 `json:"redis_hits"`
	MissItems         uint64 `json:"miss_items"`
	LoadsStarted      uint64 `json:"loads_started"`
	FlightStarted     uint64 `json:"flight_started"`
	FlightJoins       uint64 `json:"flight_joins"`
	ContentSQLBatches uint64 `json:"content_sql_batches"`
	ContentSQLRows    uint64 `json:"content_sql_rows"`
	ContentBytes      uint64 `json:"content_bytes"`
	LoadErrors        uint64 `json:"load_errors"`
	OversizeUncached  uint64 `json:"oversize_uncached"`
	RedisErrors       uint64 `json:"redis_errors"`
	InvalidPayloads   uint64 `json:"invalid_payloads"`
	BusyRejected      uint64 `json:"busy_rejected"`
	Expired           uint64 `json:"expired"`
	Evictions         uint64 `json:"evictions"`
	Inflight          int    `json:"inflight"`
	ActiveLoads       int    `json:"active_loads"`
	WaitingCallers    int    `json:"waiting_callers"`
	L1Entries         int    `json:"l1_entries"`
	L1Bytes           int64  `json:"l1_bytes"`
}

type Cache struct {
	life                      context.Context
	cancel                    context.CancelFunc
	backend                   backend
	limits                    limits
	mu                        sync.Mutex
	closed                    bool
	operations                sync.WaitGroup
	flights                   map[string]*flight
	inflight, active, waiters chan struct{}
	local                     localCache
	stats                     counters
}

type flight struct {
	done  chan struct{}
	cards map[Ref]Card
	err   error
}

// New binds shared state to application lifetime, never to a request or tx.
// Equal normalized missing batches share one bounded application-owned load.
func New(appCtx context.Context, client *redis.Client, db *gorm.DB) *Cache {
	if appCtx == nil || client == nil || db == nil {
		panic("communitycards requires application context and safe dependency pools")
	}
	return newCache(appCtx, storage{redis: client, db: db}, defaults())
}

func newCache(parent context.Context, store backend, cfg limits) *Cache {
	life, cancel := context.WithCancel(parent)
	return &Cache{life: life, cancel: cancel, backend: store, limits: cfg,
		inflight: make(chan struct{}, cfg.inflight), active: make(chan struct{}, cfg.active),
		waiters: make(chan struct{}, cfg.waiters), local: newLocal(), flights: make(map[string]*flight)}
}

func (cache *Cache) begin(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.closed || cache.life.Err() != nil {
		return nil, nil, ErrClosed
	}
	cache.operations.Add(1)
	derived, cancel := context.WithCancel(ctx)
	unlink := context.AfterFunc(cache.life, cancel)
	return derived, func() { unlink(); cancel(); cache.operations.Done() }, nil
}

func (cache *Cache) contextError(ctx context.Context) error {
	if cache.life.Err() != nil {
		return ErrClosed
	}
	return ctx.Err()
}

func normalize(refs []Ref, maximum int) ([]Ref, error) {
	if len(refs) > maximum {
		return nil, ErrInput
	}
	unique := make(map[Ref]struct{}, len(refs))
	for _, ref := range refs {
		if ref.ID == 0 {
			return nil, ErrInput
		}
		unique[ref] = struct{}{}
	}
	result := make([]Ref, 0, len(unique))
	for ref := range unique {
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID || result[i].ID == result[j].ID && result[i].Version < result[j].Version
	})
	return result, nil
}

func clone(card Card) Card {
	card.Images = append([]string{}, card.Images...)
	return card
}

// GetMany returns the complete requested set or an error; it never fills an
// unavailable directory version with a newer body or returns a partial page.
func (cache *Cache) GetMany(ctx context.Context, refs []Ref) (map[Ref]Card, error) {
	refs, err := normalize(refs, cache.limits.batch)
	if err != nil {
		return nil, err
	}
	ctx, finish, err := cache.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	cache.stats.requests.Add(1)
	result, missing := cache.cachedLocal(refs, true)
	if len(missing) != 0 {
		select {
		case cache.waiters <- struct{}{}:
			defer func() { <-cache.waiters }()
		default:
			cache.stats.busy.Add(1)
			return nil, ErrBusy
		}
		loadCtx, cancel := context.WithTimeout(ctx, cache.limits.loadTimeout)
		defer cancel()
		result, missing, err = cache.cachedRedis(loadCtx, result, missing, true)
		if err != nil {
			return nil, err
		}
		cache.stats.missing.Add(uint64(len(missing)))
		if len(missing) == 0 {
			return result, cache.contextError(ctx)
		}
		shared, err := cache.startFlight(missing)
		if err != nil {
			return nil, err
		}
		select {
		case <-loadCtx.Done():
			return nil, cache.contextError(loadCtx)
		case <-shared.done:
		}
		if err := cache.contextError(ctx); err != nil {
			return nil, err
		}
		if shared.err != nil {
			return nil, shared.err
		}
		for ref, card := range shared.cards {
			result[ref] = card
		}
	}
	if err = cache.contextError(ctx); err != nil {
		return nil, err
	}
	for ref, card := range result {
		result[ref] = clone(card)
	}
	return result, nil
}

func batchKey(refs []Ref) string {
	var result strings.Builder
	for _, ref := range refs {
		result.WriteString(strconv.FormatUint(ref.ID, 10))
		result.WriteByte(':')
		result.WriteString(strconv.FormatUint(ref.Version, 10))
		result.WriteByte(';')
	}
	return result.String()
}

func (cache *Cache) startFlight(refs []Ref) (*flight, error) {
	key := batchKey(refs)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.closed || cache.life.Err() != nil {
		return nil, ErrClosed
	}
	if existing, ok := cache.flights[key]; ok {
		cache.stats.flightJoins.Add(1)
		return existing, nil
	}
	select {
	case cache.inflight <- struct{}{}:
	default:
		cache.stats.busy.Add(1)
		return nil, ErrBusy
	}
	shared := &flight{done: make(chan struct{})}
	cache.flights[key] = shared
	cache.operations.Add(1)
	cache.stats.flightStarted.Add(1)
	cache.stats.loads.Add(1)
	ctx, cancel := context.WithTimeout(cache.life, cache.limits.loadTimeout)
	go cache.runFlight(ctx, cancel, key, shared, refs)
	return shared, nil
}

func (cache *Cache) runFlight(ctx context.Context, cancel context.CancelFunc, key string, shared *flight, refs []Ref) {
	defer cache.operations.Done()
	defer cancel()
	cards, err := cache.load(ctx, refs)
	if err != nil {
		cache.stats.loadErrors.Add(1)
	}
	cache.mu.Lock()
	shared.cards, shared.err = cards, err
	delete(cache.flights, key)
	<-cache.inflight
	close(shared.done)
	cache.mu.Unlock()
}

func (cache *Cache) cached(ctx context.Context, refs []Ref) (map[Ref]Card, []Ref, error) {
	result, remaining := cache.cachedLocal(refs, false)
	return cache.cachedRedis(ctx, result, remaining, false)
}

func (cache *Cache) cachedLocal(refs []Ref, countHits bool) (map[Ref]Card, []Ref) {
	result := make(map[Ref]Card, len(refs))
	remaining := make([]Ref, 0, len(refs))
	for _, ref := range refs {
		if card, ok := cache.local.get(ref, cache.limits.now(), &cache.stats); ok {
			if countHits {
				cache.stats.localHits.Add(1)
			}
			result[ref] = card
		} else {
			remaining = append(remaining, ref)
		}
	}
	return result, remaining
}

func (cache *Cache) cachedRedis(ctx context.Context, result map[Ref]Card, remaining []Ref, countHits bool) (map[Ref]Card, []Ref, error) {
	if len(remaining) == 0 {
		return result, nil, cache.contextError(ctx)
	}
	redisCtx, stop := context.WithTimeout(ctx, cache.limits.redisTimeout)
	values, err := cache.backend.read(redisCtx, remaining)
	stop()
	if err != nil {
		cache.stats.redisErrors.Add(1)
	}
	if canceled := cache.contextError(ctx); canceled != nil {
		return nil, nil, canceled
	}
	missing := make([]Ref, 0, len(remaining))
	for _, ref := range remaining {
		raw, ok := values[ref]
		if err == nil && ok {
			card, valid := decode(raw, ref, cache.limits.maxCardBytes)
			if valid {
				if countHits {
					cache.stats.redisHits.Add(1)
				}
				cache.local.put(ref, card, len(raw), cache.limits, &cache.stats)
				result[ref] = card
				continue
			}
			cache.stats.invalid.Add(1)
		}
		missing = append(missing, ref)
	}
	return result, missing, nil
}

func (cache *Cache) load(ctx context.Context, refs []Ref) (map[Ref]Card, error) {
	select {
	case cache.active <- struct{}{}:
		defer func() { <-cache.active }()
	case <-ctx.Done():
		return nil, cache.contextError(ctx)
	}
	// A different caller may already have filled this batch while we queued.
	result, missing, err := cache.cached(ctx, refs)
	if err != nil || len(missing) == 0 {
		return result, err
	}
	cache.stats.sqlBatches.Add(1)
	rows, facts, err := cache.backend.load(ctx, missing)
	cache.stats.sqlRows.Add(facts.rows)
	cache.stats.contentBytes.Add(facts.bytes)
	if err := cache.contextError(ctx); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	for _, ref := range missing {
		if _, ok := rows[ref]; !ok {
			return nil, ErrVersionChanged
		}
	}
	values := make([]storedValue, 0, len(missing))
	for _, ref := range missing {
		card := rows[ref]
		if card.Images == nil {
			card.Images = []string{}
		}
		result[ref] = card
		// The encoded representation cannot be smaller than these string
		// bytes. Avoid allocating another full large body just to reject it.
		if !fitsStrings(card, cache.limits.maxCardBytes) {
			cache.stats.oversize.Add(1)
			continue
		}
		raw, encodeErr := encode(ref, card)
		if encodeErr != nil || len(raw) > cache.limits.maxCardBytes {
			cache.stats.oversize.Add(1)
			continue
		}
		cache.local.put(ref, card, len(raw), cache.limits, &cache.stats)
		values = append(values, storedValue{ref: ref, body: raw, ttl: 60*time.Second + time.Duration(rand.Int63n(int64(9*time.Second)))})
	}
	if len(values) != 0 {
		redisCtx, stop := context.WithTimeout(ctx, cache.limits.redisTimeout)
		if cache.backend.write(redisCtx, values) != nil {
			cache.stats.redisErrors.Add(1)
		}
		stop()
	}
	return result, nil
}

func fitsStrings(card Card, maximum int) bool {
	remaining := maximum
	for _, field := range []string{card.Title, card.Content} {
		if len(field) > remaining {
			return false
		}
		remaining -= len(field)
	}
	for _, image := range card.Images {
		if len(image) > remaining {
			return false
		}
		remaining -= len(image)
	}
	return true
}

func (cache *Cache) Close() error {
	cache.mu.Lock()
	cache.closed = true
	cache.cancel()
	cache.mu.Unlock()
	cache.operations.Wait()
	return nil
}

func (cache *Cache) Stats() Snapshot {
	entries, size := cache.local.size()
	return Snapshot{Coalescing: true, Requests: cache.stats.requests.Load(), L1Hits: cache.stats.localHits.Load(), RedisHits: cache.stats.redisHits.Load(),
		MissItems: cache.stats.missing.Load(), LoadsStarted: cache.stats.loads.Load(), ContentSQLBatches: cache.stats.sqlBatches.Load(),
		FlightStarted: cache.stats.flightStarted.Load(), FlightJoins: cache.stats.flightJoins.Load(),
		ContentSQLRows: cache.stats.sqlRows.Load(), ContentBytes: cache.stats.contentBytes.Load(), LoadErrors: cache.stats.loadErrors.Load(),
		OversizeUncached: cache.stats.oversize.Load(), RedisErrors: cache.stats.redisErrors.Load(), InvalidPayloads: cache.stats.invalid.Load(),
		BusyRejected: cache.stats.busy.Load(), Expired: cache.stats.expired.Load(), Evictions: cache.stats.evictions.Load(),
		Inflight: len(cache.inflight), ActiveLoads: len(cache.active), WaitingCallers: len(cache.waiters), L1Entries: entries, L1Bytes: size}
}

type payload struct {
	Format  int        `json:"format"`
	ID      string     `json:"id"`
	Version string     `json:"version"`
	Title   *string    `json:"title"`
	Content *string    `json:"content"`
	Images  *[]*string `json:"images"`
}

func encode(ref Ref, card Card) ([]byte, error) {
	images := make([]*string, len(card.Images))
	for index := range card.Images {
		images[index] = &card.Images[index]
	}
	return json.Marshal(payload{1, strconv.FormatUint(ref.ID, 10), strconv.FormatUint(ref.Version, 10), &card.Title, &card.Content, &images})
}

func decode(raw []byte, ref Ref, maximum int) (Card, bool) {
	if len(raw) > maximum {
		return Card{}, false
	}
	var data payload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&data) != nil || decoder.Decode(new(any)) != io.EOF || data.Format != 1 ||
		data.ID != strconv.FormatUint(ref.ID, 10) || data.Version != strconv.FormatUint(ref.Version, 10) ||
		data.Title == nil || data.Content == nil || data.Images == nil {
		return Card{}, false
	}
	images := make([]string, len(*data.Images))
	for index, value := range *data.Images {
		if value == nil {
			return Card{}, false
		}
		images[index] = *value
	}
	return Card{Title: *data.Title, Content: *data.Content, Images: images}, true
}
