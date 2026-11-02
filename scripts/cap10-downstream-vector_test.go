package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func vectorTestControls() likeVectorControls {
	return likeVectorControls{strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)}
}

func vectorTestLimits() likeVectorLimits {
	return likeVectorLimits{MaxNewEvents: 64, MaxPendingEvents: 128, MaxTrackedBytes: 1 << 20}
}

func vectorTestEngine(t *testing.T, baseline []likeVectorFact, pairs ...likeVectorPair) *likeVectorEngine {
	t.Helper()
	if len(pairs) == 0 {
		pairs = []likeVectorPair{{NoteID: "101", UserID: "1"}}
	}
	engine, err := newLikeVector(pairs, baseline, vectorTestControls(), vectorTestLimits())
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func vectorTestFact(note, user string, version int64) likeVectorFact {
	return likeVectorFact{NoteID: note, UserID: user, Version: version, Active: int(version % 2)}
}

func vectorTestRow(t *testing.T, key likeVectorKey, id uint64, status string, received int) likeVectorSource {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"blogId": key.NoteID, "userId": key.UserID, "version": key.Version, "liked": key.Version%2 == 1})
	if err != nil {
		t.Fatal(err)
	}
	row := likeVectorSource{ID: id, EventID: key.EventID, EventType: "LIKE_CHANGED", AggregateType: "BLOG",
		AggregateID: key.NoteID, Version: key.Version, Payload: payload, DeliveryStatus: status}
	hash, err := likeVectorSourceHash(key, row)
	if err != nil {
		t.Fatal(err)
	}
	for index, projection := range []string{"follow-v1", "feed-v1", "search-v1"} {
		if index < received {
			row.Receipts = append(row.Receipts, likeVectorReceipt{Projection: projection, EventID: key.EventID, State: "IGNORED", Hash: hash})
		}
	}
	return row
}

func vectorTestCommit(t *testing.T, engine *likeVectorEngine, set *likeVectorReadSet, rows []likeVectorSource) likeVectorView {
	t.Helper()
	proposal, err := set.Evaluate(rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Commit(proposal, likeVectorCommitGate{SQLCommitted: true, SampleComplete: true}); err != nil {
		t.Fatal(err)
	}
	return proposal.View()
}

func TestLikeVectorProductionHashGoldens(t *testing.T) {
	// These literal hashes came from actual communityprojection.Decode invoked
	// through a private Go overlay. The saved oracle log/source are independent
	// of this engine; this is not a hash computed by the function under test.
	for _, fixture := range []struct {
		name, note, user, payload, expected string
		version                             int64
	}{
		{"active-v1", "101", "1", `{"blogId":"101","userId":"1","liked":true,"version":1}`, "7e4618edcd933ef2b720d7ed1ba684500f0d8a83e3fb4ebd87fb230c624389e7", 1},
		{"inactive-v2-reordered", "101", "1", ` { "version":2,"liked":false,"userId":"1","blogId":"101" } `, "5f67ab32f75062ce2a9ed51f8e667b32d22ca3ac56689ceb9901962202948210", 2},
		{"maximum-ids-version", "18446744073709551615", "18446744073709551614", `{"version":2147483647,"liked":true,"blogId":"18446744073709551615","userId":"18446744073709551614"}`, "2c2eaa01c569f2d8b514fd9b1ba407fcb6499dbbf49c245a65927ac006472153", likeVectorMaxVersion},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			key := likeVectorKey{NoteID: fixture.note, UserID: fixture.user, Version: fixture.version,
				EventID: likeVectorEventID(likeVectorPair{fixture.note, fixture.user}, fixture.version)}
			row := likeVectorSource{ID: 1, EventID: key.EventID, EventType: "LIKE_CHANGED", AggregateType: "BLOG",
				AggregateID: key.NoteID, Version: key.Version, Payload: json.RawMessage(fixture.payload)}
			hash, err := likeVectorSourceHash(key, row)
			if err != nil || hex.EncodeToString(hash[:]) != fixture.expected {
				t.Fatalf("production golden differs: hash=%x err=%v", hash, err)
			}
		})
	}
}

func TestLikeVectorLowerIDLateCommitIsNotSkipped(t *testing.T) {
	engine := vectorTestEngine(t, nil, likeVectorPair{"101", "1"}, likeVectorPair{"102", "2"})
	first, err := engine.Begin([]likeVectorFact{vectorTestFact("102", "2", 1)}, vectorTestControls())
	if err != nil || len(first.Keys()) != 1 {
		t.Fatalf("first fact vector: %v", err)
	}
	view := vectorTestCommit(t, engine, first, []likeVectorSource{vectorTestRow(t, first.Keys()[0], 101, "DONE", 3)})
	if view.Counts.Events != 1 || view.RetiredEvents != 1 {
		t.Fatalf("first commit: %+v", view)
	}
	second, err := engine.Begin([]likeVectorFact{vectorTestFact("101", "1", 1), vectorTestFact("102", "2", 1)}, vectorTestControls())
	if err != nil || len(second.Keys()) != 1 || second.Keys()[0].NoteID != "101" {
		t.Fatalf("lower source ID must still be discovered from its later fact: %v", err)
	}
	view = vectorTestCommit(t, engine, second, []likeVectorSource{vectorTestRow(t, second.Keys()[0], 100, "DONE", 3)})
	if view.Counts.Events != 2 || view.Counts.Done != 2 || view.RetiredEvents != 2 || view.PendingEvents != 0 {
		t.Fatalf("late lower ID was lost or duplicated: %+v", view)
	}
	for _, group := range view.Receipts {
		if group.Ignored != 2 || group.Missing != 0 {
			t.Fatalf("receipt counts differ: %+v", group)
		}
	}
}

func TestLikeVectorFactPresenceAndScope(t *testing.T) {
	engine := vectorTestEngine(t, nil)
	set, err := engine.Begin(nil, vectorTestControls())
	if err != nil {
		t.Fatal(err)
	}
	vectorTestCommit(t, engine, set, nil) // A never-present version-zero row.
	set, err = engine.Begin([]likeVectorFact{vectorTestFact("101", "1", 0)}, vectorTestControls())
	if err != nil {
		t.Fatal(err)
	}
	vectorTestCommit(t, engine, set, nil)
	before := engine.Snapshot()
	if _, err := engine.Begin(nil, vectorTestControls()); !errors.Is(err, errLikeVectorInvariant) {
		t.Fatal("a previously present version-zero row disappeared without failure")
	}
	if !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("failed row-disappearance read advanced state")
	}
	for _, rows := range [][]likeVectorFact{
		{vectorTestFact("101", "2", 1)},
		{vectorTestFact("999", "1", 1)},
		{vectorTestFact("101", "1", 1), vectorTestFact("101", "2", 1)},
		{{NoteID: "101", UserID: "1", Version: -1, Active: 0}},
		{{NoteID: "101", UserID: "1", Version: 1, Active: 0}},
	} {
		if _, err := engine.Begin(rows, vectorTestControls()); !errors.Is(err, errLikeVectorInvariant) {
			t.Fatalf("unexpected like row accepted: %+v %v", rows, err)
		}
	}
	advanced := vectorTestEngine(t, []likeVectorFact{vectorTestFact("101", "1", 9)})
	if _, err := advanced.Begin([]likeVectorFact{vectorTestFact("101", "1", 8)}, vectorTestControls()); !errors.Is(err, errLikeVectorInvariant) {
		t.Fatal("fact version rollback accepted")
	}
}

func TestLikeVectorAllPendingRefreshedBeforeSingleRetirement(t *testing.T) {
	engine := vectorTestEngine(t, nil, likeVectorPair{"101", "1"}, likeVectorPair{"102", "2"})
	facts := []likeVectorFact{vectorTestFact("101", "1", 1), vectorTestFact("102", "2", 1)}
	set, _ := engine.Begin(facts, vectorTestControls())
	keys := set.Keys()
	rows := []likeVectorSource{vectorTestRow(t, keys[0], 11, "PROCESSING", 3), vectorTestRow(t, keys[1], 12, "DONE", 2)}
	view := vectorTestCommit(t, engine, set, rows)
	if view.Counts.Done != 1 || view.Counts.Processing != 1 || view.RetiredEvents != 0 || view.PendingEvents != 2 || view.Receipts[2].Missing != 1 {
		t.Fatalf("receipt-before-Done incorrectly retired a source: %+v", view)
	}
	before := engine.Snapshot()
	set, _ = engine.Begin(facts, vectorTestControls())
	if _, err := set.Evaluate(rows[:1]); !errors.Is(err, errLikeVectorInvariant) || !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("partial pending refresh changed counts/state")
	}
	keys = set.Keys()
	rows = []likeVectorSource{vectorTestRow(t, keys[0], 11, "DONE", 3), vectorTestRow(t, keys[1], 12, "DONE", 3)}
	view = vectorTestCommit(t, engine, set, rows)
	if view.RetiredEvents != 2 || view.PendingEvents != 0 {
		t.Fatalf("complete sources did not retire: %+v", view)
	}
	set, _ = engine.Begin(facts, vectorTestControls())
	if len(set.Keys()) != 0 {
		t.Fatal("terminal sources kept being rescanned")
	}
	view = vectorTestCommit(t, engine, set, nil)
	if view.Counts.Events != 2 || view.Counts.Done != 2 || view.RetiredEvents != 2 || view.Receipts[0].Ignored != 2 {
		t.Fatalf("repeated confirmation double-counted a terminal event: %+v", view)
	}
}

func TestLikeVectorSourceAndReceiptMutationFailsAtomically(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		change func(*likeVectorSource)
	}{
		{"source-id", func(row *likeVectorSource) { row.ID++ }},
		{"source-case-alias", func(row *likeVectorSource) { row.EventID = strings.ToLower(row.EventID) }},
		{"source-space-alias", func(row *likeVectorSource) { row.EventID += " " }},
		{"source-aggregate", func(row *likeVectorSource) { row.AggregateID = "102" }},
		{"source-version", func(row *likeVectorSource) { row.Version = 2 }},
		{"source-payload", func(row *likeVectorSource) {
			row.Payload = json.RawMessage(`{"blogId":"101","userId":"1","liked":false,"version":1}`)
		}},
		{"done-regression", func(row *likeVectorSource) { row.DeliveryStatus = "PENDING" }},
		{"review", func(row *likeVectorSource) { row.DeliveryStatus = "NEEDS_REVIEW" }},
		{"other-status", func(row *likeVectorSource) { row.DeliveryStatus = "CUSTOM" }},
		{"receipt-disappeared", func(row *likeVectorSource) { row.Receipts = nil }},
		{"receipt-hash", func(row *likeVectorSource) { row.Receipts[0].Hash[0] ^= 1 }},
		{"receipt-applied", func(row *likeVectorSource) { row.Receipts[0].State = "APPLIED" }},
		{"receipt-alias", func(row *likeVectorSource) { row.Receipts[0].EventID += " " }},
		{"receipt-unknown-projection", func(row *likeVectorSource) { row.Receipts[0].Projection = "unknown-v1" }},
		{"receipt-duplicate", func(row *likeVectorSource) { row.Receipts[1] = row.Receipts[0] }},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			engine := vectorTestEngine(t, nil)
			facts := []likeVectorFact{vectorTestFact("101", "1", 1)}
			set, _ := engine.Begin(facts, vectorTestControls())
			vectorTestCommit(t, engine, set, []likeVectorSource{vectorTestRow(t, set.Keys()[0], 1, "DONE", 2)})
			before := engine.Snapshot()
			set, _ = engine.Begin(facts, vectorTestControls())
			row := vectorTestRow(t, set.Keys()[0], 1, "DONE", 2)
			fixture.change(&row)
			if _, err := set.Evaluate([]likeVectorSource{row}); !errors.Is(err, errLikeVectorInvariant) {
				t.Fatalf("bad source/receipt accepted: %v", err)
			}
			if !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("invalid evidence changed published state")
			}
		})
	}
}

func TestLikeVectorStrictPayloadAndSourceCoverage(t *testing.T) {
	engine := vectorTestEngine(t, nil)
	set, _ := engine.Begin([]likeVectorFact{vectorTestFact("101", "1", 1)}, vectorTestControls())
	row := vectorTestRow(t, set.Keys()[0], 1, "PENDING", 0)
	for _, raw := range []string{
		`null`, `[]`, `{"blogId":"101","userId":"1","liked":null,"version":1}`,
		`{"blogId":"101","userId":"1","liked":true,"version":1.0}`,
		`{"blogId":"101","userId":"1","liked":true,"version":1e0}`,
		`{"blogId":"101","userId":"1","liked":true,"version":1,"version":1}`,
		`{"blogId":"101","userId":"1","liked":true,"version":1,"extra":0}`,
		`{"blogId":"101","userId":"1","liked":true,"version":1} {}`,
	} {
		changed := row
		changed.Payload = json.RawMessage(raw)
		if _, err := set.Evaluate([]likeVectorSource{changed}); !errors.Is(err, errLikeVectorInvariant) {
			t.Fatalf("malformed payload accepted: %q %v", raw, err)
		}
	}
	if _, err := set.Evaluate(nil); !errors.Is(err, errLikeVectorInvariant) {
		t.Fatal("missing source was zero-filled")
	}
	if _, err := set.Evaluate([]likeVectorSource{row, row}); !errors.Is(err, errLikeVectorInvariant) {
		t.Fatal("duplicate source was accepted")
	}
	changed := row
	changed.ID = 0
	if _, err := set.Evaluate([]likeVectorSource{changed}); !errors.Is(err, errLikeVectorInvariant) {
		t.Fatal("nonpersistent source ID was accepted")
	}
}

func TestLikeVectorRejectedAndStaleCommitDoNotAdvance(t *testing.T) {
	engine := vectorTestEngine(t, nil)
	facts := []likeVectorFact{vectorTestFact("101", "1", 1)}
	before := engine.Snapshot()
	for _, gate := range []likeVectorCommitGate{{}, {SQLCommitted: true}, {SampleComplete: true}} {
		set, _ := engine.Begin(facts, vectorTestControls())
		proposal, _ := set.Evaluate([]likeVectorSource{vectorTestRow(t, set.Keys()[0], 1, "DONE", 3)})
		if err := engine.Commit(proposal, gate); !errors.Is(err, errLikeVectorRejected) || !reflect.DeepEqual(before, engine.Snapshot()) {
			t.Fatal("incomplete SQL/Kafka sample advanced a vector")
		}
		if err := engine.Commit(proposal, likeVectorCommitGate{true, true}); !errors.Is(err, errLikeVectorStale) {
			t.Fatal("rejected sample was later relabeled complete")
		}
	}
	set, _ := engine.Begin(facts, vectorTestControls())
	rows := []likeVectorSource{vectorTestRow(t, set.Keys()[0], 1, "DONE", 3)}
	proposal, _ := set.Evaluate(rows)
	other, _ := set.Evaluate(rows)
	foreign := vectorTestEngine(t, nil)
	if err := foreign.Commit(proposal, likeVectorCommitGate{true, true}); !errors.Is(err, errLikeVectorStale) {
		t.Fatal("proposal accepted by another engine")
	}
	if err := engine.Commit(proposal, likeVectorCommitGate{true, true}); err != nil {
		t.Fatal(err)
	}
	after := engine.Snapshot()
	for _, stale := range []*likeVectorProposal{proposal, other} {
		if err := engine.Commit(stale, likeVectorCommitGate{true, true}); !errors.Is(err, errLikeVectorStale) || !reflect.DeepEqual(after, engine.Snapshot()) {
			t.Fatal("repeated/stale proposal advanced state twice")
		}
	}
	if _, err := set.Evaluate(rows); !errors.Is(err, errLikeVectorStale) {
		t.Fatal("stale read set was still evaluated")
	}
}

func TestLikeVectorExplicitBoundsAndArithmetic(t *testing.T) {
	pairs := []likeVectorPair{{"101", "1"}}
	newEngine := func(limits likeVectorLimits) *likeVectorEngine {
		engine, err := newLikeVector(pairs, nil, vectorTestControls(), limits)
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}
	limits := vectorTestLimits()
	limits.MaxNewEvents = 1
	engine := newEngine(limits)
	if _, err := engine.Begin([]likeVectorFact{vectorTestFact("101", "1", 2)}, vectorTestControls()); !errors.Is(err, errLikeVectorLimit) {
		t.Fatal("unbounded new version expansion accepted")
	}
	limits = vectorTestLimits()
	limits.MaxPendingEvents = 1
	engine = newEngine(limits)
	set, _ := engine.Begin([]likeVectorFact{vectorTestFact("101", "1", 2)}, vectorTestControls())
	rows := []likeVectorSource{}
	for i, key := range set.Keys() {
		rows = append(rows, vectorTestRow(t, key, uint64(i+1), "PENDING", 0))
	}
	if _, err := set.Evaluate(rows); !errors.Is(err, errLikeVectorLimit) || engine.Snapshot().SeenEvents != 0 {
		t.Fatal("unretired cap silently dropped pending keys")
	}
	key := likeVectorKey{EventID: "LIKE:101:1:1", NoteID: "101", UserID: "1", Version: 1}
	limits = vectorTestLimits()
	limits.MaxTrackedBytes = likeVectorKeyBytes(key) - 1
	engine = newEngine(limits)
	if _, err := engine.Begin([]likeVectorFact{vectorTestFact("101", "1", 1)}, vectorTestControls()); !errors.Is(err, errLikeVectorLimit) {
		t.Fatal("requested key bytes exceeded the explicit bound")
	}
	limits.MaxTrackedBytes++
	engine = newEngine(limits)
	set, err := engine.Begin([]likeVectorFact{vectorTestFact("101", "1", 1)}, vectorTestControls())
	if err != nil {
		t.Fatal(err)
	}
	view := vectorTestCommit(t, engine, set, []likeVectorSource{vectorTestRow(t, set.Keys()[0], 1, "PENDING", 0)})
	if view.TrackedBytes != limits.MaxTrackedBytes {
		t.Fatal("byte boundary accounting differs")
	}
	if _, err := engine.Begin([]likeVectorFact{{NoteID: "101", UserID: "1", Version: likeVectorMaxInt64, Active: 1}}, vectorTestControls()); !errors.Is(err, errLikeVectorInvariant) {
		t.Fatal("overflowing business version accepted")
	}
	engine = newEngine(vectorTestLimits())
	engine.state.seen = likeVectorMaxInt64
	if _, err := engine.Begin([]likeVectorFact{vectorTestFact("101", "1", 1)}, vectorTestControls()); !errors.Is(err, errLikeVectorLimit) {
		t.Fatal("event counter overflow")
	}
	engine = newEngine(vectorTestLimits())
	engine.state.generation = ^uint64(0)
	set, _ = engine.Begin(nil, vectorTestControls())
	proposal, _ := set.Evaluate(nil)
	if err := engine.Commit(proposal, likeVectorCommitGate{true, true}); !errors.Is(err, errLikeVectorLimit) || engine.Snapshot().Generation != ^uint64(0) {
		t.Fatal("epoch overflow advanced state")
	}
}

func TestLikeVectorOwnershipAndMaintenanceBarrier(t *testing.T) {
	pairs := []likeVectorPair{{"101", "1"}}
	engine, _ := newLikeVector(pairs, nil, vectorTestControls(), vectorTestLimits())
	pairs[0] = likeVectorPair{"999", "2"}
	before := engine.Snapshot()
	controls := vectorTestControls()
	controls[2] = strings.Repeat("d", 64)
	if _, err := engine.Begin(nil, controls); !errors.Is(err, errLikeVectorInvariant) || !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("maintenance change did not invalidate the read")
	}
	facts := []likeVectorFact{vectorTestFact("101", "1", 1)}
	set, _ := engine.Begin(facts, vectorTestControls())
	facts[0].Version = 99
	keys := set.Keys()
	keys[0].EventID = "MUTATED"
	row := vectorTestRow(t, set.Keys()[0], 1, "PENDING", 1)
	proposal, err := set.Evaluate([]likeVectorSource{row})
	if err != nil {
		t.Fatal(err)
	}
	view := proposal.View()
	view.Receipts[0].Ignored = 999
	if proposal.View().Receipts[0].Ignored != 1 {
		t.Fatal("view exposed its receipt backing array")
	}
	if err := engine.Commit(proposal, likeVectorCommitGate{true, true}); err != nil {
		t.Fatal(err)
	}
	snapshot := engine.Snapshot()
	snapshot.Positions["101"] = likeVectorPosition{}
	snapshot.Pending[0].EventID = "MUTATED"
	actual := engine.Snapshot()
	if actual.Positions["101"].Version != 1 || actual.Pending[0].EventID != "LIKE:101:1:1" {
		t.Fatal("caller mutation changed owned state")
	}
}

func TestLikeVectorConcurrentProposalsHaveOneWinner(t *testing.T) {
	engine := vectorTestEngine(t, nil)
	proposals := make([]*likeVectorProposal, 8)
	for i := range proposals {
		set, _ := engine.Begin([]likeVectorFact{vectorTestFact("101", "1", 1)}, vectorTestControls())
		proposals[i], _ = set.Evaluate([]likeVectorSource{vectorTestRow(t, set.Keys()[0], 1, "DONE", 3)})
	}
	var success atomic.Int32
	var wait sync.WaitGroup
	for _, proposal := range proposals {
		wait.Add(1)
		go func(proposal *likeVectorProposal) {
			defer wait.Done()
			if err := engine.Commit(proposal, likeVectorCommitGate{true, true}); err == nil {
				success.Add(1)
			} else if !errors.Is(err, errLikeVectorStale) {
				t.Errorf("unexpected commit error: %v", err)
			}
			_ = engine.Snapshot()
		}(proposal)
	}
	wait.Wait()
	snapshot := engine.Snapshot()
	if success.Load() != 1 || snapshot.SeenEvents != 1 || snapshot.RetiredEvents != 1 || snapshot.Generation != 1 {
		t.Fatalf("concurrent proposals duplicated progress: winners=%d snapshot=%+v", success.Load(), snapshot)
	}
}

func TestLikeVectorRejectsInvalidConstruction(t *testing.T) {
	for _, pairs := range [][]likeVectorPair{nil, {{"0", "1"}}, {{"01", "1"}}, {{"18446744073709551616", "1"}}, {{"101", "1"}, {"101", "2"}}, {{"101", "1"}, {"102", "1"}}} {
		if _, err := newLikeVector(pairs, nil, vectorTestControls(), vectorTestLimits()); !errors.Is(err, errLikeVectorInput) {
			t.Fatalf("invalid fixed pairs accepted: %+v %v", pairs, err)
		}
	}
	for _, limits := range []likeVectorLimits{{}, {MaxNewEvents: likeVectorMaxEvents + 1, MaxPendingEvents: 1, MaxTrackedBytes: 1024}, {MaxNewEvents: 1, MaxPendingEvents: likeVectorMaxEvents + 1, MaxTrackedBytes: 1024}, {MaxNewEvents: 1, MaxPendingEvents: 1, MaxTrackedBytes: likeVectorMaxBytes + 1}} {
		if _, err := newLikeVector([]likeVectorPair{{"101", "1"}}, nil, vectorTestControls(), limits); !errors.Is(err, errLikeVectorInput) {
			t.Fatal("invalid explicit bounds accepted")
		}
	}
	controls := vectorTestControls()
	controls[0] = "not-a-fingerprint"
	if _, err := newLikeVector([]likeVectorPair{{"101", "1"}}, nil, controls, vectorTestLimits()); !errors.Is(err, errLikeVectorInput) {
		t.Fatal("unbound maintenance controls")
	}
}
