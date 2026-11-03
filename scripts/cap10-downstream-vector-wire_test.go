package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func wireTestFixture(t *testing.T) ([]byte, map[string]any) {
	t.Helper()
	fixture, err := json.Marshal(observerFixture(1, true))
	if err != nil {
		t.Fatal(err)
	}
	fixtureHash := sha256.Sum256(fixture)
	policy, _ := observerMode(map[string]string{"CAP10_OBSERVER_MODE": "like-vector-v1"})
	snapshot := map[string]any{"observerPolicy": policy, "auditContract": map[string]any{"contract": "like-cohort-audit-v1", "sourceScope": "selected-aggregate-id-with-blog-or-like-attribution", "hashContract": "communityprojection.Decode/LIKE/canonical-ascii-v1"}, "fixtureManifestSHA256": hex.EncodeToString(fixtureHash[:]),
		"facts": []any{map[string]any{"noteId": "10001", "userId": "1", "version": 0, "active": 0}}, "notes": []any{map[string]any{"noteId": "10001", "liked": 0, "activeRelations": 0}}, "events": []any{}, "delivery": []any{}, "receipts": []any{}, "parked": []any{}, "transitions": 0, "eventCount": 0,
		"sourceWatermark": map[string]any{"maxOutboxId": "0", "diagnosticOnly": true},
		"controlRows":     map[string]any{"projections": []any{map[string]any{"projection": "follow-v1", "rebuiltAt": nil}, map[string]any{"projection": "feed-v1", "rebuiltAt": nil}}, "search": map[string]any{"name": "search-v1", "indexAlias": "notes", "activeGeneration": "notes-build-one", "rebuiltAt": nil}}}
	proof := map[string]any{"valid": true, "factsValid": true, "downstreamConverged": true, "errors": []any{}, "startedAt": 1, "finishedAt": 2, "facts": snapshot, "derived": map[string]any{"available": true, "errors": []any{}}}
	return fixture, proof
}
func wireParse(t *testing.T, fixture []byte, proof map[string]any) *vectorBaseline {
	t.Helper()
	body, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := observerMode(map[string]string{"CAP10_OBSERVER_MODE": "like-vector-v1"})
	baseline, err := validateVectorAudit(body, fixture, vectorAuditRef{Path: "/public/reconcile.json", SHA256: "test-bound-separately"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	return baseline
}
func wireKafka() *kafkaView {
	view := &kafkaView{}
	for _, name := range groups {
		view.Groups = append(view.Groups, groupView{Group: "community." + name, Initialized: true, Partitions: []partitionView{{Partition: 0, Initialized: true}}})
	}
	return view
}

func TestVectorWireFixedPolicyAndMetadata(t *testing.T) {
	for _, mode := range []string{"full-history-v1", "like-vector-v1"} {
		policy, err := observerMode(map[string]string{"CAP10_OBSERVER_MODE": mode})
		if err != nil || policy.Scope != "full-snapshot-audit-only" || policy.Mode != mode {
			t.Fatal("policy differs")
		}
		expected := 30000
		if mode == "like-vector-v1" {
			expected = 120000
		}
		if policy.FullAuditTimeoutMS != expected {
			t.Fatal("audit budget leaked into sample budget")
		}
	}
	if policy, err := observerMode(nil); err != nil || policy.Mode != "full-history-v1" {
		t.Fatal("legacy default changed")
	}
	for _, bad := range []string{"", "vector", "LIKE-VECTOR-V1", "like-vector-v1 "} {
		if _, err := observerMode(map[string]string{"CAP10_OBSERVER_MODE": bad}); err == nil {
			t.Fatal("unsupported mode accepted")
		}
	}
	fixture, proof := wireTestFixture(t)
	baseline := wireParse(t, fixture, proof)
	if baseline.View.Counts.Events != 0 || len(baseline.Facts) != 1 || baseline.Controls != vectorMemoryControls() {
		t.Fatal("zero seeded baseline/control identity changed")
	}
	report := map[string]any{}
	policy, _ := observerMode(map[string]string{"CAP10_OBSERVER_MODE": "like-vector-v1"})
	addObserverMetadata(report, policy, baseline)
	if report["baselineCountSource"] != "bound-node-full-reconciliation" || report["baselineAuditEventCount"] != int64(0) || report["baselineAudit"] != baseline.Ref || report["observerSamplingLimits"] != (observerSamplingLimits{4000, 3000, 5, 60}) {
		t.Fatal("baseline audit reference/count source or sampling tuple differs")
	}
	if len(baseline.VectorSHA) != 64 || len(baseline.ControlsSHA) != 64 {
		t.Fatal("per-window digests missing")
	}
	if vectorPolicyMatches(json.RawMessage(`{"Mode":"like-vector-v1","fullAuditTimeoutMs":120000,"scope":"full-snapshot-audit-only"}`), policy) {
		t.Fatal("case-aliased policy keys accepted")
	}
	old := map[string]any{}
	legacy, _ := observerMode(nil)
	addObserverMetadata(old, legacy, nil)
	if _, exists := old["baselineAudit"]; exists {
		t.Fatal("legacy report invents vector baseline")
	}
}

func TestVectorWireBaselineRejectsMissingAndInvalidProof(t *testing.T) {
	for _, kind := range []string{"gate", "errors-null", "timestamp-null", "timestamp-order", "derived-null", "derived-errors-null", "snapshot-null", "policy-extra", "policy-budget", "contract-missing", "fixture-sha", "facts-missing", "facts-extra", "version-null", "version-fractional", "active-null", "parity", "note-count-null", "events-null", "event-count-null", "event-count-mismatch", "delivery-null", "delivery-state", "receipts-null", "receipt-count-null", "receipt-unknown", "parked-null", "parked-count-null", "watermark-null", "watermark-overflow", "watermark-marker", "control-null", "control-date-missing", "control-date-invalid", "control-extra-group"} {
		t.Run(kind, func(t *testing.T) {
			fixture, proof := wireTestFixture(t)
			snapshot := proof["facts"].(map[string]any)
			fact := snapshot["facts"].([]any)[0].(map[string]any)
			switch kind {
			case "gate":
				proof["factsValid"] = false
			case "errors-null":
				proof["errors"] = nil
			case "timestamp-null":
				proof["startedAt"] = nil
			case "timestamp-order":
				proof["finishedAt"] = 0
			case "derived-null":
				proof["derived"] = nil
			case "derived-errors-null":
				proof["derived"].(map[string]any)["errors"] = nil
			case "snapshot-null":
				proof["facts"] = nil
			case "policy-extra":
				snapshot["observerPolicy"] = map[string]any{"mode": "like-vector-v1", "fullAuditTimeoutMs": 120000, "scope": "full-snapshot-audit-only", "arbitrary": true}
			case "policy-budget":
				snapshot["observerPolicy"] = map[string]any{"mode": "like-vector-v1", "fullAuditTimeoutMs": 120001, "scope": "full-snapshot-audit-only"}
			case "contract-missing":
				delete(snapshot, "auditContract")
			case "fixture-sha":
				snapshot["fixtureManifestSHA256"] = "wrong"
			case "facts-missing":
				snapshot["facts"] = []any{}
			case "facts-extra":
				snapshot["facts"] = append(snapshot["facts"].([]any), map[string]any{"noteId": "10001", "userId": "2", "version": 0, "active": 0})
			case "version-null":
				fact["version"] = nil
			case "version-fractional":
				fact["version"] = 0.5
			case "active-null":
				fact["active"] = nil
			case "parity":
				fact["active"] = 1
			case "note-count-null":
				snapshot["notes"].([]any)[0].(map[string]any)["liked"] = nil
			case "events-null":
				snapshot["events"] = nil
			case "event-count-null":
				snapshot["eventCount"] = nil
			case "event-count-mismatch":
				snapshot["eventCount"] = 1
				snapshot["transitions"] = 1
			case "delivery-null":
				snapshot["delivery"] = nil
			case "delivery-state":
				snapshot["delivery"] = []any{map[string]any{"status": "PENDING", "count": 0}}
			case "receipts-null":
				snapshot["receipts"] = nil
			case "receipt-count-null":
				snapshot["receipts"] = []any{map[string]any{"projection": "follow-v1", "state": "IGNORED", "count": nil, "invalidHash": 0, "invalidProjection": 0, "invalidState": 0}}
			case "receipt-unknown":
				snapshot["receipts"] = []any{map[string]any{"projection": "other-v1", "state": "IGNORED", "count": 0, "invalidHash": 0, "invalidProjection": 0, "invalidState": 0}}
			case "parked-null":
				snapshot["parked"] = nil
			case "parked-count-null":
				snapshot["parked"] = []any{map[string]any{"projection": "follow-v1", "state": "PARKED", "count": nil}}
			case "watermark-null":
				snapshot["sourceWatermark"] = nil
			case "watermark-overflow":
				snapshot["sourceWatermark"].(map[string]any)["maxOutboxId"] = "18446744073709551615"
			case "watermark-marker":
				snapshot["sourceWatermark"].(map[string]any)["diagnosticOnly"] = false
			case "control-null":
				snapshot["controlRows"] = nil
			case "control-date-missing":
				delete(snapshot["controlRows"].(map[string]any)["projections"].([]any)[0].(map[string]any), "rebuiltAt")
			case "control-date-invalid":
				snapshot["controlRows"].(map[string]any)["projections"].([]any)[0].(map[string]any)["rebuiltAt"] = "2026-11-03T01:02:03Z"
			case "control-extra-group":
				snapshot["controlRows"].(map[string]any)["projections"] = []any{map[string]any{"projection": "other", "rebuiltAt": nil}, map[string]any{"projection": "feed-v1", "rebuiltAt": nil}}
			}
			body, _ := json.Marshal(proof)
			policy, _ := observerMode(map[string]string{"CAP10_OBSERVER_MODE": "like-vector-v1"})
			if result, err := validateVectorAudit(body, fixture, vectorAuditRef{}, policy); err == nil || result != nil {
				t.Fatal("incomplete/invalid audit accepted")
			}
		})
	}
	if vectorJSONComplete([]byte(`{"valid":true,"valid":false}`)) == nil || vectorJSONComplete([]byte(`{} {}`)) == nil {
		t.Fatal("duplicate/trailing audit accepted")
	}
}

func TestVectorWireBoundFileBytesAndNamespace(t *testing.T) {
	fixture, proof := wireTestFixture(t)
	body, _ := json.Marshal(proof)
	digest := sha256.Sum256(body)
	expected := hex.EncodeToString(digest[:])
	root := t.TempDir()
	folder := filepath.Join(root, "baseline")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, "reconcile.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	policy, _ := observerMode(map[string]string{"CAP10_OBSERVER_MODE": "like-vector-v1"})
	baseline, err := loadVectorAudit(path, expected, root, fixture, policy)
	if err != nil || baseline.Ref.Path != "baseline/reconcile.json" || baseline.Ref.SHA256 != expected {
		t.Fatal("original audit bytes reference lost")
	}
	for _, candidate := range []struct{ path, sha string }{{path, "bad"}, {filepath.Join(folder, "private.json"), expected}, {filepath.Join(root, "..", "foreign", "reconcile.json"), expected}, {path, hex.EncodeToString(make([]byte, 32))}} {
		if result, err := loadVectorAudit(candidate.path, candidate.sha, root, fixture, policy); err == nil || result != nil {
			t.Fatal("foreign/unbound audit accepted")
		}
	}
	linkFolder := filepath.Join(root, "linked")
	if err = os.Symlink(folder, linkFolder); err == nil {
		if _, err = loadVectorAudit(filepath.Join(linkFolder, "reconcile.json"), expected, root, fixture, policy); err == nil {
			t.Fatal("symlink alias accepted")
		}
	}
}

func TestVectorWireControlRowsExactKeys(t *testing.T) {
	for _, kind := range []string{"extra-top", "extra-projection", "extra-search", "case-alias"} {
		t.Run(kind, func(t *testing.T) {
			fixture, proof := wireTestFixture(t)
			controls := proof["facts"].(map[string]any)["controlRows"].(map[string]any)
			switch kind {
			case "extra-top":
				controls["extra"] = true
			case "extra-projection":
				controls["projections"].([]any)[0].(map[string]any)["extra"] = true
			case "extra-search":
				controls["search"].(map[string]any)["extra"] = true
			case "case-alias":
				search := controls["search"].(map[string]any)
				search["Name"] = search["name"]
				delete(search, "name")
			}
			body, _ := json.Marshal(proof)
			policy, _ := observerMode(map[string]string{"CAP10_OBSERVER_MODE": "like-vector-v1"})
			if _, err := validateVectorAudit(body, fixture, vectorAuditRef{}, policy); err == nil {
				t.Fatal("unknown/case-aliased control fields accepted")
			}
		})
	}
}

func TestVectorWireWatermarkExactKeys(t *testing.T) {
	for _, kind := range []string{"extra", "case-id", "case-marker"} {
		t.Run(kind, func(t *testing.T) {
			fixture, proof := wireTestFixture(t)
			watermark := proof["facts"].(map[string]any)["sourceWatermark"].(map[string]any)
			switch kind {
			case "extra":
				watermark["extra"] = true
			case "case-id":
				watermark["MaxOutboxId"] = watermark["maxOutboxId"]
				delete(watermark, "maxOutboxId")
			case "case-marker":
				watermark["DiagnosticOnly"] = watermark["diagnosticOnly"]
				delete(watermark, "diagnosticOnly")
			}
			body, _ := json.Marshal(proof)
			policy, _ := observerMode(map[string]string{"CAP10_OBSERVER_MODE": "like-vector-v1"})
			if _, err := validateVectorAudit(body, fixture, vectorAuditRef{}, policy); err == nil {
				t.Fatal("unknown/case-aliased watermark fields accepted")
			}
		})
	}
}

func TestVectorWireFreshFactoryAndWholeSampleGates(t *testing.T) {
	fixture, proof := wireTestFixture(t)
	audit := wireParse(t, fixture, proof)
	db, state, _ := vectorMemoryDatabase(t, 0)
	state.facts = []likeVectorFact{vectorTestFact("10001", "1", 0)}
	initial, engine := vectorStartup(db, audit, time.Now(), time.Now().Add(time.Second), func(context.Context) (*kafkaView, error) { return wireKafka(), nil })
	if engine == nil || len(initial.Errors) != 0 || initial.SQL.Counts.Events != audit.View.Counts.Events {
		t.Fatal("fresh baseline did not become ready")
	}
	before := engine.Snapshot()
	state.facts = []likeVectorFact{vectorTestFact("10001", "1", 1)}
	key := likeVectorKey{EventID: "LIKE:10001:1:1", NoteID: "10001", UserID: "1", Version: 1}
	state.sources[key.EventID] = vectorTestRow(t, key, 1, "DONE", 3)
	for _, kind := range []string{"error", "missing", "uninitialized", "duplicate", "late"} {
		t.Run(kind, func(t *testing.T) {
			deadline := time.Now().Add(time.Second)
			if kind == "late" {
				deadline = time.Now().Add(20 * time.Millisecond)
			}
			sample := observeVectorWithKafka(db, engine, 0, time.Now(), deadline, func(ctx context.Context) (*kafkaView, error) {
				view := wireKafka()
				switch kind {
				case "error":
					return nil, errors.New("transport failed")
				case "missing":
					return nil, nil
				case "uninitialized":
					view.Groups[0].Initialized = false
				case "duplicate":
					view.Groups[1] = view.Groups[0]
				case "late":
					<-ctx.Done()
				}
				return view, nil
			})
			if len(sample.Errors) == 0 || !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("incomplete/late Kafka observation published SQL candidate")
			}
		})
	}
	sample := observeVectorWithKafka(db, engine, 0, time.Now(), time.Now().Add(time.Second), func(context.Context) (*kafkaView, error) { return wireKafka(), nil })
	if len(sample.Errors) > 0 || sample.SQL.Counts.Events != 1 || engine.Snapshot().SeenEvents != 1 || sample.VectorSQL == nil || !sample.VectorSQL.SQLCommitted {
		t.Fatal("complete SQL/Kafka sample did not publish once")
	}
}

func TestVectorWireFreshFactoryRejectsChangesAndCommitFailure(t *testing.T) {
	for _, kind := range []string{"changed-fact", "missing-fact", "extra-fact", "changed-control", "commit", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			fixture, proof := wireTestFixture(t)
			audit := wireParse(t, fixture, proof)
			db, state, _ := vectorMemoryDatabase(t, 0)
			state.facts = []likeVectorFact{vectorTestFact("10001", "1", 0)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "changed-fact":
				state.facts[0].Version = 1
				state.facts[0].Active = 1
			case "missing-fact":
				state.facts = nil
			case "extra-fact":
				state.facts = append(state.facts, vectorTestFact("10001", "2", 0))
			case "changed-control":
				audit.Controls[0] = "changed"
			case "commit":
				state.commitError = errors.New("read commit failed")
			case "canceled":
				cancel()
			}
			if engine, err := createVectorEngine(ctx, db, audit); err == nil || engine != nil {
				t.Fatal("unverified/failed fresh baseline created runtime engine")
			}
		})
	}
}

func TestVectorWireNullableControlCanonicalDates(t *testing.T) {
	fixture, proof := wireTestFixture(t)
	controls := proof["facts"].(map[string]any)["controlRows"].(map[string]any)
	controls["projections"].([]any)[0].(map[string]any)["rebuiltAt"] = "2026-11-03 01:02:03.456000"
	baseline := wireParse(t, fixture, proof)
	oracle := sha256.Sum256([]byte(`{"Projection":"follow-v1","RebuiltAt":{"String":"2026-11-03 01:02:03.456000","Valid":true}}`))
	if baseline.Controls[0] != hex.EncodeToString(oracle[:]) {
		t.Fatal("raw SQL6 nullable control fingerprint differs from independent canonical literal")
	}
}

func TestVectorWireCumulativeBaselineAndStageDelta(t *testing.T) {
	fixture, proof := wireTestFixture(t)
	snapshot := proof["facts"].(map[string]any)
	snapshot["facts"].([]any)[0].(map[string]any)["version"] = 2
	snapshot["transitions"], snapshot["eventCount"] = 2, 2
	snapshot["sourceWatermark"].(map[string]any)["maxOutboxId"] = "2"
	snapshot["events"] = []any{map[string]any{"noteId": "10001", "userId": "1", "count": 2, "distinctVersions": 2, "minVersion": 1, "maxVersion": 2, "invalid": 0}}
	snapshot["delivery"] = []any{map[string]any{"status": "DONE", "count": 2}}
	var receipts []any
	for _, group := range groups {
		receipts = append(receipts, map[string]any{"projection": group, "state": "IGNORED", "count": 2, "invalidHash": 0, "invalidProjection": 0, "invalidState": 0})
	}
	snapshot["receipts"] = receipts
	audit := wireParse(t, fixture, proof)
	db, state, _ := vectorMemoryDatabase(t, 0)
	state.facts = []likeVectorFact{vectorTestFact("10001", "1", 2)}
	initial, engine := vectorStartup(db, audit, time.Now(), time.Now().Add(time.Second), func(context.Context) (*kafkaView, error) { return wireKafka(), nil })
	if engine == nil || len(initial.Errors) > 0 || initial.SQL.Counts.Events != 2 || engine.Snapshot().SeenEvents != 0 {
		t.Fatal("readiness lost audited historical count or injected it into stage engine")
	}
	state.facts = []likeVectorFact{vectorTestFact("10001", "1", 3)}
	key := likeVectorKey{EventID: "LIKE:10001:1:3", NoteID: "10001", UserID: "1", Version: 3}
	state.sources[key.EventID] = vectorTestRow(t, key, 103, "DONE", 3)
	observed := observeVectorWithKafka(db, engine, 0, time.Now(), time.Now().Add(time.Second), func(context.Context) (*kafkaView, error) { return wireKafka(), nil })
	if len(observed.Errors) > 0 || observed.SQL.Counts.Events != 1 || engine.Snapshot().SeenEvents != 1 || audit.View.Counts.Events != 2 {
		t.Fatal("stage counters include historical versions or baseline mutated")
	}
}
