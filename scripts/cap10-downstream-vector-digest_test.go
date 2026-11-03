package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sampleDigestHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func sampleDigestControl(t *testing.T, raw string) (likeVectorControls, string) {
	t.Helper()
	var rows vectorAuditControls
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatal(err)
	}
	controls, err := vectorControlHashes(&rows)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(controls)
	if err != nil {
		t.Fatal(err)
	}
	return controls, sampleDigestHex(body)
}

func sampleDigestAssertAbsent(t *testing.T, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"digestContract", "factVectorSHA256", "controlsSHA256"} {
		if strings.Contains(string(body), `"`+key+`"`) {
			t.Fatalf("unaccepted/baseline/legacy evidence acquired %s: %s", key, body)
		}
	}
}

func TestVectorSampleDigestOriginalGoldenAndSameTotalSwap(t *testing.T) {
	pairs := []likeVectorPair{{"101", "1"}, {"102", "2"}}
	facts := []likeVectorFact{vectorTestFact("101", "1", 2), vectorTestFact("102", "2", 0)}
	actual, err := vectorDigest(pairs, facts)
	// These literal hashes come from the retained original df0 Go parser of the
	// independent Node-produced synthetic baseline, before this output extension.
	const original = "a2771e7accea38676cae9c590242b7303e4276bcaa1b75830883e103f8c3e219"
	const canonical = `[{"noteId":"101","userId":"1","present":true,"version":2,"active":0},{"noteId":"102","userId":"2","present":true,"version":0,"active":0}]`
	if err != nil || actual != original || sampleDigestHex([]byte(canonical)) != original {
		t.Fatal("original canonical vector golden changed")
	}
	reordered, err := vectorDigest(pairs, []likeVectorFact{facts[1], facts[0]})
	if err != nil || reordered != original {
		t.Fatal("SQL row order displaced fixture order")
	}
	swapped := []likeVectorFact{vectorTestFact("101", "1", 0), vectorTestFact("102", "2", 2)}
	other, err := vectorDigest(pairs, swapped)
	if err != nil || other == original || swapped[0].Version+swapped[1].Version != facts[0].Version+facts[1].Version || swapped[0].Active+swapped[1].Active != facts[0].Active+facts[1].Active {
		t.Fatal("same totals concealed a different exact pair vector")
	}
	_, controls := sampleDigestControl(t, `{"projections":[{"projection":"follow-v1","rebuiltAt":null},{"projection":"feed-v1","rebuiltAt":"2026-11-03 12:30:01.123456"}],"search":{"name":"search-v1","indexAlias":"notes","activeGeneration":"notes-current","rebuiltAt":"2026-11-03 12:30:02.000000"}}`)
	if controls != "3fc537e3bd53c443fade83dcb43da20ec92606c02e3687c20b9eb635dfd9b812" {
		t.Fatal("original nullable SQL6 control golden changed")
	}
}

func TestVectorSampleDigestControlEscapingAndNullIdentity(t *testing.T) {
	raw := `{"projections":[{"projection":"feed-v1","rebuiltAt":null},{"projection":"follow-v1","rebuiltAt":"2026-11-03 12:30:01.123000"}],"search":{"name":"search-v1","indexAlias":"<>&\"\\中文\u2028\u2029","activeGeneration":"","rebuiltAt":null}}`
	controls, digest := sampleDigestControl(t, raw)
	// Independent literal bytes expose encoding/json's HTML and line-separator
	// escaping; hashing the raw Node controlRows JSON would produce a different ID.
	const search = `{"Name":"search-v1","IndexAlias":"\u003c\u003e\u0026\"\\中文\u2028\u2029","ActiveGeneration":"","RebuiltAt":{"String":"","Valid":false}}`
	if controls[2] != sampleDigestHex([]byte(search)) {
		t.Fatal("control identity drifted from original Go escaping or key casing")
	}
	var rows vectorAuditControls
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatal(err)
	}
	rows.Projections[0], rows.Projections[1] = rows.Projections[1], rows.Projections[0]
	body, _ := json.Marshal(rows)
	_, reordered := sampleDigestControl(t, string(body))
	if reordered != digest {
		t.Fatal("raw control projection order changed fixed follow/feed/search order")
	}
	rows.Search.RebuiltAt = json.RawMessage(`"2026-11-03 12:30:02.000000"`)
	body, _ = json.Marshal(rows)
	_, nonnull := sampleDigestControl(t, string(body))
	if nonnull == digest {
		t.Fatal("SQL NULL and explicit date became the same identity")
	}
}

func TestVectorSampleDigestDiagnosticViewNeverPublishes(t *testing.T) {
	db, _, engine := vectorMemoryDatabase(t, 1)
	before := engine.Snapshot()
	candidate, stats, err := readLikeVectorSQL(context.Background(), db, engine, 0)
	if err != nil || !stats.SQLCommitted || stats.Queries != 8 || stats.SourcePrepares != 1 {
		t.Fatalf("digest added SQL or lost the original committed candidate: stats=%+v err=%v", stats, err)
	}
	sampleDigestAssertAbsent(t, candidate.SQLView())
	if err := candidate.Accept(false); !errors.Is(err, errLikeVectorRejected) {
		t.Fatal("whole sample rejection changed")
	}
	if err := candidate.Accept(true); !errors.Is(err, errLikeVectorStale) {
		t.Fatal("consumed failed proposal became accepted")
	}
	sampleDigestAssertAbsent(t, candidate.SQLView())
	if !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("diagnostic identity publication moved the engine")
	}
}

func TestVectorSampleDigestSuccessfulSameRROnly(t *testing.T) {
	db, state, engine := vectorMemoryDatabase(t, 1)
	factSHA, _ := vectorDigest(engine.pairs, []likeVectorFact{vectorTestFact("101", "1", 1)})
	controlBytes, _ := json.Marshal(vectorMemoryControls())
	observed := observeVectorWithKafka(db, engine, 0, time.Now(), time.Now().Add(time.Second), func(context.Context) (*kafkaView, error) {
		// This changes the later DB test state after the actual RR read/commit.
		// The publication must describe the accepted SQL proposal, not a new read.
		state.mu.Lock()
		state.facts = []likeVectorFact{vectorTestFact("101", "1", 2)}
		state.mu.Unlock()
		return wireKafka(), nil
	})
	if len(observed.Errors) != 0 || len(observed.Violations) != 0 || observed.SQL == nil || observed.SQL.DigestContract != "like-vector-digest-v1" || observed.SQL.FactVectorSHA256 != factSHA || observed.SQL.ControlsSHA256 != sampleDigestHex(controlBytes) || engine.Snapshot().SeenEvents != 1 {
		t.Fatalf("successful whole sample did not publish its own RR identity: %+v", observed)
	}
	if observed.VectorSQL.Queries != 8 || observed.VectorSQL.SourcePrepares != 1 {
		t.Fatal("identity extension issued additional SQL")
	}
	body, _ := json.Marshal(observed)
	var wire struct {
		SQL map[string]any `json:"sql"`
	}
	if json.Unmarshal(body, &wire) != nil || wire.SQL["digestContract"] != "like-vector-digest-v1" || wire.SQL["factVectorSHA256"] != factSHA || wire.SQL["controlsSHA256"] != sampleDigestHex(controlBytes) {
		t.Fatal("published JSON lost or moved the exact three-field contract")
	}
}

func TestVectorSampleDigestSQLKafkaDeadlineAndStaleFailures(t *testing.T) {
	for _, kind := range []string{"commit", "missing-source", "bad-source-hash", "control", "kafka-error", "kafka-missing", "kafka-uninitialized", "kafka-duplicate", "kafka-late", "already-expired", "stale-epoch"} {
		t.Run(kind, func(t *testing.T) {
			db, state, engine := vectorMemoryDatabase(t, 1)
			before := engine.Snapshot()
			deadline := time.Now().Add(time.Second)
			switch kind {
			case "commit":
				state.commitError = errors.New("read commit acknowledgement lost")
			case "missing-source":
				delete(state.sources, "LIKE:101:1:1")
			case "bad-source-hash":
				row := state.sources["LIKE:101:1:1"]
				row.Receipts[0].Hash[0] ^= 1
				state.sources[row.EventID] = row
			case "control":
				engine.controls[0] = strings.Repeat("f", 64)
			case "kafka-late":
				deadline = time.Now().Add(5 * time.Millisecond)
			case "already-expired":
				deadline = time.Now().Add(-time.Second)
			}
			observed := observeVectorWithKafka(db, engine, 0, time.Now(), deadline, func(ctx context.Context) (*kafkaView, error) {
				view := wireKafka()
				switch kind {
				case "kafka-error":
					return nil, errors.New("transport error")
				case "kafka-missing":
					return nil, nil
				case "kafka-uninitialized":
					view.Groups[0].Initialized = false
				case "kafka-duplicate":
					view.Groups[1] = view.Groups[0]
				case "kafka-late":
					<-ctx.Done()
				case "stale-epoch":
					other, _, err := readLikeVectorSQL(ctx, db, engine, 0)
					if err != nil || other.Accept(true) != nil {
						t.Fatal("test competing accepted epoch could not publish")
					}
					before = engine.Snapshot()
				}
				return view, nil
			})
			if len(observed.Errors) == 0 || !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("failed/stale whole sample advanced the engine")
			}
			sampleDigestAssertAbsent(t, observed)
		})
	}
}

func TestVectorSampleDigestCanceledSQLNeverReturnsCandidate(t *testing.T) {
	db, state, engine := vectorMemoryDatabase(t, 1)
	before := engine.Snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state.cancelOnPrepare = cancel
	candidate, _, err := readLikeVectorSQL(ctx, db, engine, 0)
	if err == nil || candidate != nil || !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("canceled SQL produced a borrowable identity candidate")
	}
}

func TestVectorSampleDigestBaselineAndLegacyJSONUnchanged(t *testing.T) {
	fixture, proof := wireTestFixture(t)
	audit := wireParse(t, fixture, proof)
	db, state, _ := vectorMemoryDatabase(t, 0)
	state.facts = []likeVectorFact{vectorTestFact("10001", "1", 0)}
	initial, engine := vectorStartup(db, audit, time.Now(), time.Now().Add(time.Second), func(context.Context) (*kafkaView, error) { return wireKafka(), nil })
	if engine == nil || len(initial.Errors) != 0 || initial.SQL.Counts.Events != 0 {
		t.Fatal("existing readiness contract changed")
	}
	sampleDigestAssertAbsent(t, initial)
	zero, _ := json.Marshal(sqlView{})
	const legacy = `{"startedAt":"0001-01-01T00:00:00Z","finishedAt":"0001-01-01T00:00:00Z","afterWatermark":0,"maxOutboxId":0,"counts":{"events":0,"pending":0,"processing":0,"done":0,"needsReview":0,"other":0},"receipts":null,"parked":0}`
	if string(zero) != legacy {
		t.Fatal("omitempty extension changed existing legacy SQL JSON")
	}
	metadata := map[string]any{}
	policy, _ := observerMode(map[string]string{"CAP10_OBSERVER_MODE": "like-vector-v1"})
	addObserverMetadata(metadata, policy, audit)
	if metadata["baselineVectorSHA256"] != audit.VectorSHA || metadata["baselineControlsSHA256"] != audit.ControlsSHA || metadata["baselineCountSource"] != "bound-node-full-reconciliation" {
		t.Fatal("baseline digest/count provenance changed")
	}
	sampleDigestAssertAbsent(t, metadata)
}
