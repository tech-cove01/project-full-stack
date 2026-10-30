package communitycards

import (
	"container/list"
	"sync"
	"time"
)

type localEntry struct {
	ref    Ref
	card   Card
	bytes  int
	expire time.Time
}

type localCache struct {
	mu      sync.Mutex
	entries map[Ref]*list.Element
	order   *list.List
	bytes   int64
}

func newLocal() localCache {
	return localCache{entries: make(map[Ref]*list.Element), order: list.New()}
}

func (local *localCache) remove(element *list.Element) {
	entry := element.Value.(localEntry)
	delete(local.entries, entry.ref)
	local.bytes -= int64(entry.bytes)
	local.order.Remove(element)
}

func (local *localCache) get(ref Ref, now time.Time, stats *counters) (Card, bool) {
	local.mu.Lock()
	defer local.mu.Unlock()
	element, ok := local.entries[ref]
	if !ok {
		return Card{}, false
	}
	entry := element.Value.(localEntry)
	if !now.Before(entry.expire) {
		local.remove(element)
		stats.expired.Add(1)
		return Card{}, false
	}
	local.order.MoveToFront(element)
	return clone(entry.card), true
}

func (local *localCache) put(ref Ref, card Card, size int, cfg limits, stats *counters) {
	if cfg.localTTL <= 0 || cfg.localEntries <= 0 || int64(size) > cfg.localBytes {
		return
	}
	local.mu.Lock()
	defer local.mu.Unlock()
	if old, ok := local.entries[ref]; ok {
		local.remove(old)
	}
	element := local.order.PushFront(localEntry{ref: ref, card: clone(card), bytes: size, expire: cfg.now().Add(cfg.localTTL)})
	local.entries[ref] = element
	local.bytes += int64(size)
	for len(local.entries) > cfg.localEntries || local.bytes > cfg.localBytes {
		local.remove(local.order.Back())
		stats.evictions.Add(1)
	}
}

func (local *localCache) size() (int, int64) {
	local.mu.Lock()
	defer local.mu.Unlock()
	return len(local.entries), local.bytes
}
