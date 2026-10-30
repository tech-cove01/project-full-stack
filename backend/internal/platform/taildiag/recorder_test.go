package taildiag

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDisabledRecorderIsANoOpAndStageAllowlistIsClosed(t *testing.T) {
	ctx := context.Background()
	derived, finish := Start(ctx, nil, true)
	if derived != ctx || Begin(ctx, FeedFacts, 20) != nil || Begin(ctx, Stage(255), 1) != nil {
		t.Fatal("disabled diagnostics must leave the context unchanged")
	}
	finish(true)
	var span *Span
	span.End(false)
	var recorder *Recorder
	if recorder.Snapshot().Enabled {
		t.Fatal("nil recorder must be disabled")
	}
}

func TestBoundedSlowRingTruncatesAndCopiesWithoutExportingContextValues(t *testing.T) {
	recorder := New()
	for i := 0; i < MaxSlowTraces+9; i++ {
		ctx := context.WithValue(context.Background(), struct{ secret bool }{true}, "private-token-user-sql-body")
		ctx, finish := Start(ctx, recorder, true)
		fromContext(ctx).started = time.Now().Add(-SlowThreshold - time.Millisecond)
		for n := 0; n < MaxSpans+3; n++ {
			span := Begin(ctx, CountsRedisRead, 20)
			span.End(false)
			span.End(true)
		}
		finish(false)
		finish(true)
	}
	out := recorder.Snapshot()
	if out.Requests != MaxSlowTraces+9 || out.LatestSequence != MaxSlowTraces+9 || len(out.SlowTraces) != MaxSlowTraces ||
		out.SlowTraces[0].Sequence != 10 || out.SlowTraces[len(out.SlowTraces)-1].Sequence != out.LatestSequence {
		t.Fatal("slow ring is not bounded or monotonic")
	}
	for _, trace := range out.SlowTraces {
		if trace.Scope != "feed.http" || !trace.Truncated || len(trace.Spans) != MaxSpans || trace.Error {
			t.Fatal("trace bound, scope or once-only completion changed")
		}
	}
	count := uint64((MaxSlowTraces + 9) * (MaxSpans + 3))
	if out.Stages[CountsRedisRead].Count != count || out.Stages[CountsRedisRead].Active != 0 || out.Stages[CountsRedisRead].Items != 20*count || out.Stages[CountsRedisRead].Errors != 0 {
		t.Fatal("aggregate count must include truncated events exactly once")
	}
	encoded, err := json.Marshal(out)
	if err != nil || strings.Contains(string(encoded), "private-token-user-sql-body") {
		t.Fatal("context values must never be exported")
	}
	out.SlowTraces[0].Spans[0].Name = "modified-by-reader"
	if recorder.Snapshot().SlowTraces[0].Spans[0].Name == "modified-by-reader" {
		t.Fatal("snapshot must not expose mutable internal slices")
	}
}

func TestActiveCanceledStagesAndNestedScopeAreObservedWithoutSleep(t *testing.T) {
	recorder := New()
	ctx, cancel := context.WithCancel(context.Background())
	ctx, finish := Start(ctx, recorder, true)
	inner, innerFinish := Start(ctx, recorder, false)
	if inner != ctx {
		t.Fatal("service must share the enclosing HTTP trace")
	}
	fromContext(ctx).started = time.Now().Add(-SlowThreshold - time.Millisecond)
	span := Begin(ctx, PublicationWait, 0)
	if recorder.Snapshot().Stages[PublicationWait].Active != 1 {
		t.Fatal("an in-progress wait must be visible before it finishes")
	}
	cancel()
	span.End(ctx.Err() != nil)
	innerFinish(false)
	if recorder.Snapshot().Requests != 0 {
		t.Fatal("inner service must not complete the enclosing trace")
	}
	finish(true)
	out := recorder.Snapshot()
	if out.Requests != 1 || out.Stages[PublicationWait].Active != 0 || out.Stages[PublicationWait].Errors != 1 ||
		len(out.SlowTraces) != 1 || !out.SlowTraces[0].Canceled || !out.SlowTraces[0].Error {
		t.Fatal("cancellation or scope was lost")
	}
}

func TestConcurrentAggregatesRemainBounded(t *testing.T) {
	recorder := New()
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ctx, finish := Start(context.Background(), recorder, false)
			for j := 0; j < 50; j++ {
				span := Begin(ctx, SQLCall, 0)
				span.End(false)
				_ = recorder.Snapshot()
			}
			finish(false)
		}()
	}
	workers.Wait()
	out := recorder.Snapshot()
	if out.Requests != 16 || out.Stages[SQLCall].Count != 800 || out.Stages[SQLCall].Active != 0 || len(out.SlowTraces) > MaxSlowTraces {
		t.Fatal("concurrent observations lost counts or escaped bounds")
	}
}
