package taildiag

import "sync"

// The registry bounds retained clocks independently of incoming concurrency.
// Its lock protects memory only, never backend I/O or trace serialization.
type activeStage struct {
	mu                sync.Mutex
	starts            [MaxActivePerStage]int64
	next              int
	active, untracked int64
	overflows         uint64
}

func (live *activeStage) begin(tick int64) int {
	live.mu.Lock()
	defer live.mu.Unlock()
	live.active++
	for offset := 0; offset < MaxActivePerStage; offset++ {
		index := (live.next + offset) % MaxActivePerStage
		if live.starts[index] == 0 {
			live.starts[index] = tick
			live.next = (index + 1) % MaxActivePerStage
			return index
		}
	}
	live.untracked++
	live.overflows++
	return -1
}

func (live *activeStage) end(slot int) {
	live.mu.Lock()
	defer live.mu.Unlock()
	live.active--
	if slot < 0 {
		live.untracked--
	} else {
		live.starts[slot] = 0
	}
}

func (live *activeStage) snapshot(now int64) (active int64, tracked int, untracked, oldest int64, overflows uint64) {
	live.mu.Lock()
	defer live.mu.Unlock()
	for _, started := range live.starts {
		if started != 0 {
			tracked++
			if age := now - started; age > oldest {
				oldest = age
			}
		}
	}
	return live.active, tracked, live.untracked, oldest, live.overflows
}
