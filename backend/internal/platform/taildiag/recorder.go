// Package taildiag records bounded, explicitly enabled Feed timing evidence.
// Stages are a fixed allowlist. It never accepts identities, SQL, cache keys,
// command arguments, request bodies, URLs or arbitrary metric labels.
package taildiag

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type Stage uint8

const (
	FeedService Stage = iota
	FeedFacts
	FeedCounts
	AuthResolve
	SQLBegin
	SQLHold
	SQLStatement
	SQLCall
	SQLExec
	SQLPrepare
	RedisCommand
	RedisPipeline
	RedisDial
	CountsMany
	PublicationWait
	PublicationHold
	CountsRedisRead
	CountsRedisWrite
	CountsRedisDelete
	CountsColdSQL
	stageCount
)

var names = [...]string{
	"feed.service", "feed.facts", "feed.counts", "auth.resolve",
	"sql.begin_acquire_roundtrip", "sql.transaction_hold", "sql.statement_total",
	"sql.call_until_rows", "sql.exec_call", "sql.prepare_call",
	"redis.command_total", "redis.pipeline_total", "redis.dial",
	"counts.get_many", "counts.publication_wait", "counts.publication_hold",
	"counts.redis_read", "counts.redis_write", "counts.redis_delete", "counts.cold_sql",
}

const (
	MaxSlowTraces = 32
	MaxSpans      = 256
	SlowThreshold = 100 * time.Millisecond
)

type aggregate struct {
	count, total, max, errors, items atomic.Uint64
	active                           atomic.Int64
}

type Aggregate struct {
	Name    string `json:"name"`
	Count   uint64 `json:"count"`
	TotalNS uint64 `json:"total_ns"`
	MaxNS   uint64 `json:"max_ns"`
	Errors  uint64 `json:"errors"`
	Items   uint64 `json:"items"`
	Active  int64  `json:"active"`
}

type Timing struct {
	Name       string `json:"name"`
	OffsetNS   int64  `json:"offset_ns"`
	DurationNS int64  `json:"duration_ns"`
	Error      bool   `json:"error"`
	Items      int    `json:"items"`
}

type SlowTrace struct {
	Sequence  uint64   `json:"sequence"`
	Scope     string   `json:"scope"`
	StartAt   string   `json:"start_at"`
	EndAt     string   `json:"end_at"`
	TotalNS   int64    `json:"total_ns"`
	Error     bool     `json:"error"`
	Canceled  bool     `json:"canceled"`
	Truncated bool     `json:"truncated"`
	Spans     []Timing `json:"spans"`
}

type Snapshot struct {
	Enabled             bool        `json:"enabled"`
	SlowThresholdMS     int64       `json:"slow_threshold_ms"`
	MaxSlowTraces       int         `json:"max_slow_traces"`
	MaxSpansPerTrace    int         `json:"max_spans_per_trace"`
	LatestSequence      uint64      `json:"latest_sequence"`
	Requests            uint64      `json:"requests"`
	Stages              []Aggregate `json:"stages"`
	SlowTraces          []SlowTrace `json:"slow_traces"`
	MissingMeasurements []string    `json:"missing_measurements"`
}

type Recorder struct {
	stages   [stageCount]aggregate
	requests atomic.Uint64
	mu       sync.Mutex
	sequence uint64
	slow     [MaxSlowTraces]SlowTrace
}

func New() *Recorder { return &Recorder{} }

type traceKey struct{}

type request struct {
	recorder  *Recorder
	started   time.Time
	scope     string
	mu        sync.Mutex
	spans     [MaxSpans]Timing
	count     int
	truncated bool
}

// Start reuses an enclosing HTTP trace. Only the creator publishes the trace;
// direct Service calls can be observed without producing a second HTTP record.
func Start(ctx context.Context, recorder *Recorder, http bool) (context.Context, func(bool)) {
	if recorder == nil || fromContext(ctx) != nil {
		return ctx, func(bool) {}
	}
	scope := "feed.service"
	if http {
		scope = "feed.http"
	}
	trace := &request{recorder: recorder, started: time.Now(), scope: scope}
	derived := context.WithValue(ctx, traceKey{}, trace)
	var once sync.Once
	return derived, func(failed bool) {
		once.Do(func() {
			end := time.Now()
			recorder.requests.Add(1)
			if end.Sub(trace.started) < SlowThreshold {
				return
			}
			trace.mu.Lock()
			spans := append([]Timing{}, trace.spans[:trace.count]...)
			truncated := trace.truncated
			trace.mu.Unlock()
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			recorder.sequence++
			recorder.slow[(recorder.sequence-1)%MaxSlowTraces] = SlowTrace{
				Sequence: recorder.sequence, Scope: trace.scope,
				StartAt: trace.started.UTC().Format(time.RFC3339Nano), EndAt: end.UTC().Format(time.RFC3339Nano),
				TotalNS: int64(end.Sub(trace.started)), Error: failed, Canceled: ctx.Err() != nil,
				Truncated: truncated, Spans: spans,
			}
		})
	}
}

func fromContext(ctx context.Context) *request {
	trace, _ := ctx.Value(traceKey{}).(*request)
	return trace
}

type Span struct {
	trace   *request
	stage   Stage
	started time.Time
	items   int
	once    sync.Once
}

// Begin returns nil outside enabled Feed contexts. End is safe on nil, so the
// production path does not allocate timers, maps or metric labels when disabled.
func Begin(ctx context.Context, stage Stage, items int) *Span {
	trace := fromContext(ctx)
	if trace == nil || stage >= stageCount {
		return nil
	}
	trace.recorder.stages[stage].active.Add(1)
	return &Span{trace: trace, stage: stage, started: time.Now(), items: items}
}

func (span *Span) End(failed bool) {
	if span == nil {
		return
	}
	span.once.Do(func() {
		span.trace.recorder.stages[span.stage].active.Add(-1)
		span.trace.record(span.stage, span.started, time.Since(span.started), failed, span.items)
	})
}

// Record measures an existing boundary, e.g. GORM's total statement callback.
// It does not attempt to infer a pure connection-pool wait or server SQL time.
func Record(ctx context.Context, stage Stage, started time.Time, failed bool, items int) {
	if trace := fromContext(ctx); trace != nil && stage < stageCount {
		trace.record(stage, started, time.Since(started), failed, items)
	}
}

func (trace *request) record(stage Stage, started time.Time, duration time.Duration, failed bool, items int) {
	if duration < 0 {
		duration = 0
	}
	stat := &trace.recorder.stages[stage]
	stat.count.Add(1)
	stat.total.Add(uint64(duration))
	if failed {
		stat.errors.Add(1)
	}
	if items > 0 {
		stat.items.Add(uint64(items))
	}
	for previous := stat.max.Load(); uint64(duration) > previous; previous = stat.max.Load() {
		if stat.max.CompareAndSwap(previous, uint64(duration)) {
			break
		}
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if trace.count == MaxSpans {
		trace.truncated = true
		return
	}
	trace.spans[trace.count] = Timing{Name: names[stage], OffsetNS: int64(started.Sub(trace.started)),
		DurationNS: int64(duration), Error: failed, Items: items}
	trace.count++
}

func (recorder *Recorder) Snapshot() Snapshot {
	if recorder == nil {
		return Snapshot{Enabled: false}
	}
	out := Snapshot{Enabled: true, SlowThresholdMS: SlowThreshold.Milliseconds(), MaxSlowTraces: MaxSlowTraces,
		MaxSpansPerTrace: MaxSpans, Requests: recorder.requests.Load(), Stages: make([]Aggregate, 0, stageCount),
		SlowTraces: []SlowTrace{}, MissingMeasurements: []string{
			"sql.begin_acquire_roundtrip includes pool acquisition and BEGIN roundtrip; pure per-request SQL pool wait is not separately measured",
			"sql.transaction_hold spans successful BeginTx return through Commit/Rollback return, including server commit/rollback roundtrip",
			"sql.statement_total includes pool acquisition when outside a transaction, execution, transfer and GORM scanning; sql.call_until_rows ends before full row consumption",
			"redis.command_total includes pool acquisition, transport and command RTT; pure per-request Redis pool wait and server execution are not separated",
			"stage counters and pool snapshots are concurrent observations, not an atomic per-request attribution; nested stages must not be added as disjoint totals",
			"trace.canceled reflects the outer observed HTTP or direct-service context; inner deadline failures are reflected by stage errors without exporting error text",
			"feed.http covers the Gin handler including authentication and JSON serialization, not client-observed network RTT or time before handler entry",
		}}
	for stage := Stage(0); stage < stageCount; stage++ {
		stat := &recorder.stages[stage]
		out.Stages = append(out.Stages, Aggregate{Name: names[stage], Count: stat.count.Load(), TotalNS: stat.total.Load(),
			MaxNS: stat.max.Load(), Errors: stat.errors.Load(), Items: stat.items.Load(), Active: stat.active.Load()})
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	out.LatestSequence = recorder.sequence
	start := uint64(1)
	if recorder.sequence >= MaxSlowTraces {
		start = recorder.sequence - MaxSlowTraces + 1
	}
	for sequence := start; sequence <= recorder.sequence; sequence++ {
		trace := recorder.slow[(sequence-1)%MaxSlowTraces]
		trace.Spans = append([]Timing{}, trace.Spans...)
		out.SlowTraces = append(out.SlowTraces, trace)
	}
	return out
}
