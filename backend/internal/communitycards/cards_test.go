package communitycards

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeBackend struct {
	mu            sync.Mutex
	cards         map[Ref]Card
	values        map[Ref][]byte
	writes        []storedValue
	readCalls     atomic.Int64
	loadCalls     atomic.Int64
	writeCalls    atomic.Int64
	readHook      func(context.Context, []Ref) error
	loadHook      func(context.Context, []Ref) error
	afterLoadHook func(context.Context, []Ref) error
	writeHook     func(context.Context, []storedValue) error
}

func fakeStore() *fakeBackend {
	return &fakeBackend{cards: make(map[Ref]Card), values: make(map[Ref][]byte)}
}

func (store *fakeBackend) read(ctx context.Context, refs []Ref) (map[Ref][]byte, error) {
	store.readCalls.Add(1)
	if store.readHook != nil {
		if err := store.readHook(ctx, refs); err != nil {
			return nil, err
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	values := make(map[Ref][]byte)
	for _, ref := range refs {
		if value, ok := store.values[ref]; ok {
			values[ref] = append([]byte(nil), value...)
		}
	}
	return values, nil
}

func (store *fakeBackend) load(ctx context.Context, refs []Ref) (map[Ref]Card, loadFacts, error) {
	store.loadCalls.Add(1)
	if store.loadHook != nil {
		if err := store.loadHook(ctx, refs); err != nil {
			return nil, loadFacts{}, err
		}
	}
	store.mu.Lock()
	rows, facts := make(map[Ref]Card), loadFacts{}
	for _, ref := range refs {
		if card, ok := store.cards[ref]; ok {
			rows[ref] = clone(card)
			facts.rows++
			facts.bytes += uint64(len(card.Title) + len(card.Content) + len(strings.Join(card.Images, ",")))
		}
	}
	store.mu.Unlock()
	if store.afterLoadHook != nil {
		if err := store.afterLoadHook(ctx, refs); err != nil {
			return nil, facts, err
		}
	}
	return rows, facts, nil
}

func (store *fakeBackend) write(ctx context.Context, values []storedValue) error {
	store.writeCalls.Add(1)
	if store.writeHook != nil {
		if err := store.writeHook(ctx, values); err != nil {
			return err
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, value := range values {
		store.values[value.ref] = append([]byte(nil), value.body...)
		store.writes = append(store.writes, value)
	}
	return nil
}

func testCache(t *testing.T, store *fakeBackend, cfg limits) *Cache {
	t.Helper()
	cache := newCache(context.Background(), store, cfg)
	t.Cleanup(func() { _ = cache.Close() })
	return cache
}

func get(t *testing.T, cache *Cache, refs ...Ref) map[Ref]Card {
	t.Helper()
	result, err := cache.GetMany(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func await[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("operation failed to converge")
		var zero T
		return zero
	}
}

func TestBatchColdL1RedisAndVersionZero(t *testing.T) {
	store, cfg := fakeStore(), defaults()
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	cfg.now = func() time.Time { return time.Unix(0, clock.Load()) }
	cache := testCache(t, store, cfg)
	refs := make([]Ref, 20)
	for index := range refs {
		refs[index] = Ref{ID: uint64(index + 1), Version: uint64(index)}
		store.cards[refs[index]] = Card{Title: fmt.Sprint(index), Content: "内容", Images: []string{"a", "b"}}
	}
	first := get(t, cache, refs...)
	if len(first) != 20 || store.loadCalls.Load() != 1 || store.writeCalls.Load() != 1 {
		t.Fatalf("cold batch did not use one content load/write: %#v", cache.Stats())
	}
	for _, value := range store.writes {
		if value.ttl < 60*time.Second || value.ttl >= 69*time.Second {
			t.Fatalf("TTL outside fixed jitter range: %s", value.ttl)
		}
	}
	// Caller mutation cannot change another caller, Redis or L1.
	first[refs[0]].Images[0] = "changed"
	delete(first, refs[1])
	if warm := get(t, cache, refs...); len(warm) != 20 || warm[refs[0]].Images[0] != "a" {
		t.Fatal("caller mutated cached values")
	}
	clock.Add(int64(cfg.localTTL))
	if next := get(t, cache, refs...); len(next) != 20 || next[refs[0]].Images[0] != "a" {
		t.Fatal("Redis did not restore expired local batch")
	}
	stats := cache.Stats()
	if stats.ContentSQLBatches != 1 || stats.ContentSQLRows != 20 || stats.L1Hits != 20 || stats.RedisHits != 20 || stats.Expired != 20 || !stats.Coalescing || stats.FlightStarted != 1 || stats.FlightJoins != 0 {
		t.Fatalf("wrong batch stats: %#v", stats)
	}
	if key(refs[0]) != "community:note-card:v1:1:0" {
		t.Fatal("historical version zero key was changed")
	}
}

func TestNormalizeAndRejectInvalidInputsBeforeIO(t *testing.T) {
	store := fakeStore()
	cache := testCache(t, store, defaults())
	store.cards[Ref{1, 0}] = Card{Title: "zero"}
	if got := get(t, cache, Ref{1, 0}, Ref{1, 0}); len(got) != 1 {
		t.Fatal("duplicate references were not normalized")
	}
	for _, input := range [][]Ref{{{0, 1}}, make([]Ref, 51)} {
		if result, err := cache.GetMany(context.Background(), input); !errors.Is(err, ErrInput) || result != nil {
			t.Fatalf("invalid input accepted: %v %v", result, err)
		}
	}
	if store.loadCalls.Load() != 1 {
		t.Fatal("invalid input reached storage")
	}
	refs, err := normalize([]Ref{{2, 1}, {1, 2}, {1, 0}, {2, 1}}, 50)
	if err != nil || !reflect.DeepEqual(refs, []Ref{{1, 0}, {1, 2}, {2, 1}}) {
		t.Fatalf("noncanonical normalization: %v %v", refs, err)
	}
}

func TestBadPayloadsMissWithoutReturningPoison(t *testing.T) {
	ref := Ref{1, 0}
	valid := `{"format":1,"id":"1","version":"0","title":"","content":"body","images":[]}`
	for name, bad := range map[string]string{
		"leading ID":      strings.Replace(valid, `"id":"1"`, `"id":"01"`, 1),
		"leading version": strings.Replace(valid, `"version":"0"`, `"version":"00"`, 1),
		"wrong version":   strings.Replace(valid, `"version":"0"`, `"version":"1"`, 1),
		"number ID":       strings.Replace(valid, `"id":"1"`, `"id":1`, 1),
		"null title":      strings.Replace(valid, `"title":""`, `"title":null`, 1),
		"null images":     strings.Replace(valid, `"images":[]`, `"images":null`, 1),
		"wrong images":    strings.Replace(valid, `"images":[]`, `"images":[1]`, 1),
		"null image item": strings.Replace(valid, `"images":[]`, `"images":[null]`, 1),
		"missing content": strings.Replace(valid, `"content":"body",`, ``, 1),
		"format":          strings.Replace(valid, `"format":1`, `"format":2`, 1),
		"unknown":         strings.Replace(valid, `"format":1`, `"format":1,"personal":true`, 1),
		"trailing":        valid + `{}`,
		"bad JSON":        `{`,
	} {
		t.Run(name, func(t *testing.T) {
			store := fakeStore()
			store.values[ref], store.cards[ref] = []byte(bad), Card{Title: "actual", Images: []string{}}
			cache := testCache(t, store, defaults())
			if got := get(t, cache, ref); got[ref].Title != "actual" || cache.Stats().InvalidPayloads == 0 || cache.Stats().ContentSQLBatches != 1 {
				t.Fatalf("bad value not replaced via content load: %#v", cache.Stats())
			}
		})
	}
	if card, ok := decode([]byte(valid), ref, 8192); !ok || card.Images == nil || card.Title != "" {
		t.Fatal("valid empty title/images were rejected")
	}
}

func TestMissingVersionReturnsWholeErrorWithoutPartialFill(t *testing.T) {
	store := fakeStore()
	store.cards[Ref{1, 1}] = Card{Title: "exists"}
	cache := testCache(t, store, defaults())
	if result, err := cache.GetMany(context.Background(), []Ref{{1, 1}, {2, 1}}); !errors.Is(err, ErrVersionChanged) || result != nil {
		t.Fatalf("version miss returned partial/new content: %#v %v", result, err)
	}
	if store.writeCalls.Load() != 0 || cache.Stats().L1Entries != 0 {
		t.Fatal("partially successful batch was cached")
	}
}

func TestSQLAndRedisFailuresAndOversize(t *testing.T) {
	t.Run("SQL error", func(t *testing.T) {
		store, failure := fakeStore(), errors.New("SQL failed")
		store.loadHook = func(context.Context, []Ref) error { return failure }
		cache := testCache(t, store, defaults())
		if result, err := cache.GetMany(context.Background(), []Ref{{1, 1}}); !errors.Is(err, failure) || result != nil {
			t.Fatalf("SQL failure masked: %v %v", result, err)
		}
		if cache.Stats().ContentSQLBatches != 1 || cache.Stats().L1Entries != 0 || store.writeCalls.Load() != 0 {
			t.Fatal("SQL error cached an empty result")
		}
	})
	t.Run("Redis degraded", func(t *testing.T) {
		store := fakeStore()
		store.cards[Ref{1, 1}] = Card{Title: "actual"}
		store.readHook = func(context.Context, []Ref) error { return errors.New("Redis read failed") }
		store.writeHook = func(context.Context, []storedValue) error { return errors.New("Redis write failed") }
		cache := testCache(t, store, defaults())
		if got := get(t, cache, Ref{1, 1}); got[Ref{1, 1}].Title != "actual" || cache.Stats().RedisErrors != 3 {
			t.Fatalf("Redis outage prevented bounded SQL result: %#v", cache.Stats())
		}
	})
	t.Run("legal oversize", func(t *testing.T) {
		store := fakeStore()
		ref := Ref{1, 1}
		body := strings.Repeat("大", 8192)
		store.cards[ref] = Card{Title: "large", Content: body}
		cache := testCache(t, store, defaults())
		for index := 0; index < 2; index++ {
			if got := get(t, cache, ref); got[ref].Content != body {
				t.Fatal("legal large content was truncated")
			}
		}
		if cache.Stats().OversizeUncached != 2 || cache.Stats().L1Entries != 0 || store.writeCalls.Load() != 0 || store.loadCalls.Load() != 2 {
			t.Fatal("oversized card entered a cache")
		}
	})
}

func TestLocalBudgetsExpiryAndMutableCopies(t *testing.T) {
	local, cfg, stats := newLocal(), defaults(), counters{}
	cfg.localEntries, cfg.localBytes = 2, 15
	now := time.Now()
	cfg.now = func() time.Time { return now }
	card := Card{Images: []string{"original"}}
	local.put(Ref{1, 1}, card, 10, cfg, &stats)
	card.Images[0] = "caller changed"
	local.put(Ref{2, 1}, Card{}, 10, cfg, &stats)
	if count, size := local.size(); count != 1 || size != 10 || stats.evictions.Load() != 1 {
		t.Fatal("byte budget did not evict oldest entry")
	}
	cfg.localBytes = 100
	local.put(Ref{3, 1}, card, 10, cfg, &stats)
	local.put(Ref{4, 1}, card, 10, cfg, &stats)
	if count, size := local.size(); count != 2 || size != 20 {
		t.Fatal("entry budget was exceeded")
	}
	if _, ok := local.get(Ref{4, 1}, now.Add(cfg.localTTL), &stats); ok || stats.expired.Load() != 1 {
		t.Fatal("entry survived its fixed local expiry")
	}
}

func TestBlockedWriteDoesNotHoldOtherKeys(t *testing.T) {
	store := fakeStore()
	a, b := Ref{1, 1}, Ref{2, 1}
	store.cards[a], store.cards[b] = Card{Title: "A"}, Card{Title: "B"}
	cache := testCache(t, store, defaults())
	get(t, cache, b)
	entered, release := make(chan struct{}), make(chan struct{})
	store.writeHook = func(ctx context.Context, values []storedValue) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { _, err := cache.GetMany(context.Background(), []Ref{a}); done <- err }()
	await(t, entered)
	other := make(chan error, 1)
	go func() { _, err := cache.GetMany(context.Background(), []Ref{b}); other <- err }()
	if err := await(t, other); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestBoundsCancellationAndCloseConverge(t *testing.T) {
	store, cfg := fakeStore(), defaults()
	cfg.active, cfg.inflight, cfg.waiters = 1, 1, 2
	entered := make(chan struct{}, 1)
	store.loadHook = func(ctx context.Context, refs []Ref) error {
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	cache := testCache(t, store, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := cache.GetMany(ctx, []Ref{{1, 1}}); done <- err }()
	await(t, entered)
	if _, err := cache.GetMany(context.Background(), []Ref{{2, 1}}); !errors.Is(err, ErrBusy) {
		t.Fatalf("inflight budget was not enforced: %v", err)
	}
	cancel()
	if err := await(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation did not stop its own wait: %v", err)
	}
	if stats := cache.Stats(); stats.Inflight != 1 || stats.ActiveLoads != 1 {
		t.Fatal("first caller cancellation canceled the shared application task")
	}
	go func() { _, err := cache.GetMany(context.Background(), []Ref{{1, 1}}); done <- err }()
	waitStats(t, cache, func(stats Snapshot) bool { return stats.FlightJoins == 1 })
	closed := make(chan error, 1)
	go func() { closed <- cache.Close() }()
	if err := await(t, done); !errors.Is(err, ErrClosed) {
		t.Fatalf("application close not distinguished: %v", err)
	}
	if err := await(t, closed); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.GetMany(context.Background(), []Ref{{4, 1}}); !errors.Is(err, ErrClosed) {
		t.Fatal("closed cache accepted a new operation")
	}
	if stats := cache.Stats(); stats.Inflight != 0 || stats.ActiveLoads != 0 || stats.WaitingCallers != 0 {
		t.Fatalf("close left admissions behind: %#v", stats)
	}
}

func waitStats(t *testing.T, cache *Cache, predicate func(Snapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !predicate(cache.Stats()) {
		if time.Now().After(deadline) {
			t.Fatalf("controlled cache state was not reached: %#v", cache.Stats())
		}
		runtime.Gosched()
	}
}

func TestSameNormalizedBatchSharesLoadAfterFirstCallerCancels(t *testing.T) {
	store := fakeStore()
	a, b := Ref{1, 1}, Ref{2, 0}
	store.cards[a], store.cards[b] = Card{Title: "A", Images: []string{"a"}}, Card{Title: "B", Images: []string{"b"}}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	store.loadHook = func(ctx context.Context, refs []Ref) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	cache := testCache(t, store, defaults())
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := cache.GetMany(firstCtx, []Ref{b, a, a}); first <- err }()
	await(t, entered)
	cancelFirst()
	if err := await(t, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller did not cancel independently: %v", err)
	}
	results, failures := make(chan map[Ref]Card, 3), make(chan error, 3)
	for index := 0; index < 3; index++ {
		go func() {
			result, err := cache.GetMany(context.Background(), []Ref{a, b})
			results <- result
			failures <- err
		}()
	}
	waitStats(t, cache, func(stats Snapshot) bool { return stats.FlightJoins == 3 })
	close(release)
	var previous map[Ref]Card
	for index := 0; index < 3; index++ {
		if err := await(t, failures); err != nil {
			t.Fatal(err)
		}
		result := await(t, results)
		if len(result) != 2 || result[a].Images[0] != "a" {
			t.Fatal("shared task returned incomplete/aliased results")
		}
		if previous != nil {
			previous[a].Images[0] = "mutated"
			if result[a].Images[0] != "a" {
				t.Fatal("coalesced callers share mutable images")
			}
		}
		previous = result
	}
	stats := cache.Stats()
	if store.loadCalls.Load() != 1 || stats.FlightStarted != 1 || stats.ContentSQLBatches != 1 || stats.ContentSQLRows != 2 || stats.L1Hits != 0 || stats.RedisHits != 0 {
		t.Fatalf("same batch did not share exactly one content SQL: %#v", stats)
	}
}

func TestOverlappingDifferentBatchesAreIndependent(t *testing.T) {
	store := fakeStore()
	a, b, c := Ref{1, 1}, Ref{2, 1}, Ref{3, 1}
	for _, ref := range []Ref{a, b, c} {
		store.cards[ref] = Card{Title: fmt.Sprint(ref.ID)}
	}
	entered, release := make(chan struct{}, 2), make(chan struct{})
	store.loadHook = func(ctx context.Context, refs []Ref) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	cache := testCache(t, store, defaults())
	done := make(chan error, 2)
	go func() { _, err := cache.GetMany(context.Background(), []Ref{a, b}); done <- err }()
	await(t, entered)
	go func() { _, err := cache.GetMany(context.Background(), []Ref{b, c}); done <- err }()
	await(t, entered)
	close(release)
	for index := 0; index < 2; index++ {
		if err := await(t, done); err != nil {
			t.Fatal(err)
		}
	}
	if stats := cache.Stats(); stats.FlightStarted != 2 || stats.FlightJoins != 0 || stats.ContentSQLBatches != 2 || stats.ContentSQLRows != 4 {
		t.Fatalf("different batches were incorrectly serialized/merged: %#v", stats)
	}
}

func TestCapturedOldVersionCannotOverwriteConcurrentNewVersion(t *testing.T) {
	store := fakeStore()
	old, current := Ref{1, 1}, Ref{1, 2}
	store.cards[old], store.cards[current] = Card{Title: "v1 captured"}, Card{Title: "v2 current"}
	captured, release := make(chan struct{}), make(chan struct{})
	// This is a storage-level captured-response injection, not an edit API.
	store.afterLoadHook = func(ctx context.Context, refs []Ref) error {
		if refs[0] == old {
			close(captured)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	cache := testCache(t, store, defaults())
	done := make(chan error, 1)
	go func() { _, err := cache.GetMany(context.Background(), []Ref{old}); done <- err }()
	await(t, captured)
	if result := get(t, cache, current); result[current].Title != "v2 current" {
		t.Fatal("current version could not load while old response was paused")
	}
	close(release)
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	if result := get(t, cache, current); result[current].Title != "v2 current" {
		t.Fatal("late old load overwrote current L1 content")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for ref, title := range map[Ref]string{old: "v1 captured", current: "v2 current"} {
		card, ok := decode(store.values[ref], ref, defaults().maxCardBytes)
		if !ok || card.Title != title {
			t.Fatal("late response escaped its own version namespace")
		}
	}
	if len(store.writes) != 2 || key(old) == key(current) {
		t.Fatal("versioned responses shared a Redis key")
	}
	for _, write := range store.writes {
		if write.ttl < time.Minute || write.ttl >= 69*time.Second {
			t.Fatal("late version write changed its bounded TTL")
		}
	}
}

func TestWaitingBudgetBeforeRedisAndDeadlinePropagation(t *testing.T) {
	store, cfg := fakeStore(), defaults()
	cfg.waiters = 1
	entered := make(chan time.Duration, 1)
	store.readHook = func(ctx context.Context, refs []Ref) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("Redis missing deadline")
		}
		entered <- time.Until(deadline)
		<-ctx.Done()
		return ctx.Err()
	}
	cache := testCache(t, store, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := cache.GetMany(ctx, []Ref{{1, 1}}); done <- err }()
	if budget := await(t, entered); budget <= 0 || budget > 250*time.Millisecond {
		t.Fatalf("Redis operation escaped its budget: %s", budget)
	}
	if _, err := cache.GetMany(context.Background(), []Ref{{2, 1}}); !errors.Is(err, ErrBusy) {
		t.Fatalf("waiting caller budget was not enforced before Redis: %v", err)
	}
	cancel()
	if err := await(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if store.readCalls.Load() != 1 || store.loadCalls.Load() != 0 {
		t.Fatal("rejected/canceled caller started a content load")
	}
}

func TestActiveQueueAndWholeLoadDeadline(t *testing.T) {
	store, cfg := fakeStore(), defaults()
	cfg.active, cfg.inflight = 1, 3
	entered, secondLookup := make(chan time.Duration, 1), make(chan struct{})
	var secondLookupOnce sync.Once
	store.loadHook = func(ctx context.Context, refs []Ref) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("content SQL missing deadline")
		}
		entered <- time.Until(deadline)
		<-ctx.Done()
		return ctx.Err()
	}
	store.readHook = func(ctx context.Context, refs []Ref) error {
		if refs[0].ID == 2 {
			secondLookupOnce.Do(func() { close(secondLookup) })
		}
		return nil
	}
	cache := testCache(t, store, cfg)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := cache.GetMany(firstCtx, []Ref{{1, 1}}); first <- err }()
	if budget := await(t, entered); budget <= 0 || budget > 2*time.Second {
		t.Fatalf("content SQL escaped the whole load budget: %s", budget)
	}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	go func() { _, err := cache.GetMany(secondCtx, []Ref{{2, 1}}); second <- err }()
	await(t, secondLookup)
	waitStats(t, cache, func(stats Snapshot) bool {
		return stats.FlightStarted == 2 && stats.Inflight == 2 && stats.ActiveLoads == 1
	})
	cancelSecond()
	if err := await(t, second); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued caller did not cancel: %v", err)
	}
	if stats := cache.Stats(); store.loadCalls.Load() != 1 || stats.Inflight != 2 || stats.ActiveLoads != 1 {
		t.Fatalf("caller cancellation removed queued shared work or exceeded active bound: %#v", stats)
	}
	cancelFirst()
	if err := await(t, first); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if stats := cache.Stats(); stats.Inflight != 0 || stats.ActiveLoads != 0 || stats.WaitingCallers != 0 || store.loadCalls.Load() != 1 {
		t.Fatalf("close did not converge queued shared work within active bound: %#v", stats)
	}
}
