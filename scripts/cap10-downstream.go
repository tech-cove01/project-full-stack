// CAP10 finite read-only outbox/receipt/Kafka observer. Build from backend's
// existing module; no service, schema, producer or business process is started.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	platform "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/segmentio/kafka-go"
)

var groups = []string{"follow-v1", "feed-v1", "search-v1"}

type counts struct {
	Events      int64 `json:"events"`
	Pending     int64 `json:"pending"`
	Processing  int64 `json:"processing"`
	Done        int64 `json:"done"`
	NeedsReview int64 `json:"needsReview"`
	Other       int64 `json:"other"`
}
type receipt struct {
	Projection string `json:"projection"`
	Missing    int64  `json:"missing"`
	Applied    int64  `json:"applied"`
	Ignored    int64  `json:"ignored"`
}
type sqlView struct {
	StartedAt      time.Time `json:"startedAt"`
	FinishedAt     time.Time `json:"finishedAt"`
	AfterWatermark int64     `json:"afterWatermark"`
	MaxOutboxID    int64     `json:"maxOutboxId"`
	Counts         counts    `json:"counts"`
	Receipts       []receipt `json:"receipts"`
	Parked         int64     `json:"parked"`
}
type partitionView struct {
	Partition   int   `json:"partition"`
	First       int64 `json:"firstOffset"`
	End         int64 `json:"endOffset"`
	Committed   int64 `json:"committedOffset"`
	Lag         int64 `json:"lag"`
	Initialized bool  `json:"initialized"`
}
type groupView struct {
	Group       string          `json:"group"`
	Partitions  []partitionView `json:"partitions"`
	Lag         int64           `json:"lag"`
	Initialized bool            `json:"initialized"`
}
type kafkaView struct {
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt time.Time   `json:"finishedAt"`
	Topic      string      `json:"topic"`
	Groups     []groupView `json:"groups"`
}
type sample struct {
	At             time.Time  `json:"at"`
	ElapsedSeconds float64    `json:"elapsedSeconds"`
	DurationMS     float64    `json:"durationMs"`
	SQL            *sqlView   `json:"sql"`
	Kafka          *kafkaView `json:"kafka"`
	Errors         []string   `json:"errors"`
	Violations     []string   `json:"semanticViolations"`
}

// The like-vector-v1 accounting engine is deliberately not connected to main,
// SQL or Kafka yet. A caller must supply a separately audited baseline and may
// publish a proposal only after its read-only transaction and complete sample
// succeeded. Legacy observation remains the default and only wired method.
const (
	likeVectorMaxVersion = int64(1<<31 - 1) // The business version column is INT.
	likeVectorMaxEvents  = int64(1 << 20)
	likeVectorMaxBytes   = int64(1 << 30)
	likeVectorMaxInt64   = int64(1<<63 - 1)
)

var (
	errLikeVectorInput     = errors.New("invalid like vector input")
	errLikeVectorInvariant = errors.New("like vector invariant violation")
	errLikeVectorLimit     = errors.New("like vector observation bound exceeded")
	errLikeVectorStale     = errors.New("stale or consumed like vector proposal")
	errLikeVectorRejected  = errors.New("like vector sample was not accepted")
)

type likeVectorPair struct{ NoteID, UserID string }
type likeVectorFact struct {
	NoteID, UserID string
	Version        int64
	Active         int
}
type likeVectorPosition struct {
	Version int64
	Present bool
}

// Fingerprints cover the original nullable control columns, not wall-clock
// sampling time. The SQL adapter must create these in the same RR snapshot.
type likeVectorControls [3]string
type likeVectorLimits struct {
	MaxNewEvents     int64
	MaxPendingEvents int64
	MaxTrackedBytes  int64
}
type likeVectorKey struct {
	EventID, NoteID, UserID string
	Version                 int64
	SourceID                uint64
	SourceHash              [32]byte
	ReceiptMask             uint8
	SourceDone              bool
}
type likeVectorReceipt struct {
	Projection, EventID, State string
	Hash                       [32]byte
}
type likeVectorSource struct {
	ID                         uint64
	EventID, EventType         string
	AggregateType, AggregateID string
	Version                    int64
	Payload                    json.RawMessage
	DeliveryStatus             string
	Receipts                   []likeVectorReceipt
}
type likeVectorState struct {
	generation    uint64
	positions     map[string]likeVectorPosition
	pending       map[string]likeVectorKey
	seen, retired int64
	trackedBytes  int64
}
type likeVectorEngine struct {
	mu       sync.Mutex
	pairs    []likeVectorPair
	users    map[string]string
	controls likeVectorControls
	limits   likeVectorLimits
	state    *likeVectorState
}
type likeVectorReadSet struct {
	owner     *likeVectorEngine
	base      *likeVectorState
	positions map[string]likeVectorPosition
	keys      map[string]likeVectorKey
	newEvents int64
	readBytes int64
}
type likeVectorView struct {
	Counts              counts
	Receipts            []receipt
	RetiredEvents       int64
	PendingEvents       int64
	TrackedBytes        int64
	ReadBytesUpperBound int64
}
type likeVectorProposal struct {
	owner    *likeVectorEngine
	base     *likeVectorState
	next     *likeVectorState
	view     likeVectorView
	consumed bool
}
type likeVectorCommitGate struct{ SQLCommitted, SampleComplete bool }
type likeVectorSnapshot struct {
	Generation                uint64
	Positions                 map[string]likeVectorPosition
	Pending                   []likeVectorKey
	SeenEvents, RetiredEvents int64
	TrackedBytes              int64
}

func likeVectorID(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == value
}

func likeVectorControlValid(controls likeVectorControls) bool {
	for _, value := range controls {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != value {
			return false
		}
	}
	return true
}

func newLikeVector(pairs []likeVectorPair, baseline []likeVectorFact, controls likeVectorControls, limits likeVectorLimits) (*likeVectorEngine, error) {
	if len(pairs) < 1 || len(pairs) > 4096 || !likeVectorControlValid(controls) ||
		limits.MaxNewEvents < 1 || limits.MaxNewEvents > likeVectorMaxEvents ||
		limits.MaxPendingEvents < 1 || limits.MaxPendingEvents > likeVectorMaxEvents ||
		limits.MaxTrackedBytes < 1 || limits.MaxTrackedBytes > likeVectorMaxBytes {
		return nil, errLikeVectorInput
	}
	engine := &likeVectorEngine{pairs: append([]likeVectorPair(nil), pairs...), users: make(map[string]string, len(pairs)), controls: controls, limits: limits}
	users := make(map[string]bool, len(pairs))
	for _, pair := range engine.pairs {
		if !likeVectorID(pair.NoteID) || !likeVectorID(pair.UserID) || engine.users[pair.NoteID] != "" || users[pair.UserID] {
			return nil, errLikeVectorInput
		}
		engine.users[pair.NoteID], users[pair.UserID] = pair.UserID, true
	}
	sort.Slice(engine.pairs, func(i, j int) bool { return engine.pairs[i].NoteID < engine.pairs[j].NoteID })
	positions, err := engine.factPositions(baseline, nil)
	if err != nil {
		return nil, err
	}
	engine.state = &likeVectorState{positions: positions, pending: make(map[string]likeVectorKey)}
	return engine, nil
}

func (engine *likeVectorEngine) factPositions(rows []likeVectorFact, previous map[string]likeVectorPosition) (map[string]likeVectorPosition, error) {
	if len(rows) > len(engine.pairs) {
		return nil, fmt.Errorf("%w: extra_fact_rows", errLikeVectorInvariant)
	}
	positions := make(map[string]likeVectorPosition, len(engine.pairs))
	for _, pair := range engine.pairs {
		positions[pair.NoteID] = likeVectorPosition{}
	}
	for _, row := range rows {
		current, expected := positions[row.NoteID]
		if !expected || current.Present || row.UserID != engine.users[row.NoteID] || row.Version < 0 ||
			row.Version > likeVectorMaxVersion || row.Active != int(row.Version%2) {
			return nil, fmt.Errorf("%w: fact_identity_or_version", errLikeVectorInvariant)
		}
		positions[row.NoteID] = likeVectorPosition{Version: row.Version, Present: true}
	}
	for note, before := range previous {
		after := positions[note]
		if before.Present && !after.Present || after.Version < before.Version {
			return nil, fmt.Errorf("%w: fact_disappeared_or_regressed", errLikeVectorInvariant)
		}
	}
	return positions, nil
}

func likeVectorEventID(pair likeVectorPair, version int64) string {
	return "LIKE:" + pair.NoteID + ":" + pair.UserID + ":" + strconv.FormatInt(version, 10)
}

// This conservative logical accounting bounds owned key/state allocations; it
// is not an RSS measurement. External process/resource supervision is required.
func likeVectorKeyBytes(key likeVectorKey) int64 {
	return int64(512 + len(key.EventID) + len(key.NoteID) + len(key.UserID))
}

func (engine *likeVectorEngine) Begin(rows []likeVectorFact, controls likeVectorControls) (*likeVectorReadSet, error) {
	if engine == nil || controls != engine.controls {
		return nil, fmt.Errorf("%w: maintenance_control_changed", errLikeVectorInvariant)
	}
	engine.mu.Lock()
	base := engine.state
	engine.mu.Unlock()
	positions, err := engine.factPositions(rows, base.positions)
	if err != nil {
		return nil, err
	}
	var added int64
	readBytes := base.trackedBytes
	for _, pair := range engine.pairs {
		version := positions[pair.NoteID].Version
		delta := version - base.positions[pair.NoteID].Version
		if delta > engine.limits.MaxNewEvents-added {
			return nil, fmt.Errorf("%w: new_event_count", errLikeVectorLimit)
		}
		added += delta
		if delta > 0 {
			upper := likeVectorKeyBytes(likeVectorKey{EventID: likeVectorEventID(pair, version), NoteID: pair.NoteID, UserID: pair.UserID})
			if delta > (engine.limits.MaxTrackedBytes-readBytes)/upper {
				return nil, fmt.Errorf("%w: requested_key_bytes", errLikeVectorLimit)
			}
			readBytes += delta * upper
		}
	}
	if added > likeVectorMaxInt64-base.seen {
		return nil, fmt.Errorf("%w: event_counter_overflow", errLikeVectorLimit)
	}
	keys := make(map[string]likeVectorKey, len(base.pending)+int(added))
	for id, key := range base.pending {
		keys[id] = key
	}
	for _, pair := range engine.pairs {
		for version := base.positions[pair.NoteID].Version + 1; version <= positions[pair.NoteID].Version; version++ {
			key := likeVectorKey{EventID: likeVectorEventID(pair, version), NoteID: pair.NoteID, UserID: pair.UserID, Version: version}
			keys[key.EventID] = key
		}
	}
	return &likeVectorReadSet{owner: engine, base: base, positions: positions, keys: keys, newEvents: added, readBytes: readBytes}, nil
}

func (set *likeVectorReadSet) Keys() []likeVectorKey {
	if set == nil {
		return nil
	}
	keys := make([]likeVectorKey, 0, len(set.keys))
	for _, key := range set.keys {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].EventID < keys[j].EventID })
	return keys
}

// Match production communityprojection.Decode's LIKE canonical payload and its
// six-field envelope hash. Golden fixtures are obtained from that actual codec,
// not generated by this helper. No internal import may break single-file build.
func likeVectorSourceHash(key likeVectorKey, row likeVectorSource) ([32]byte, error) {
	var zero [32]byte
	if row.ID == 0 || row.EventID != key.EventID || row.EventType != "LIKE_CHANGED" || row.AggregateType != "BLOG" ||
		row.AggregateID != key.NoteID || row.Version != key.Version || len(row.Payload) == 0 || len(row.Payload) > 1<<20 || !utf8.Valid(row.Payload) {
		return zero, fmt.Errorf("%w: source_identity", errLikeVectorInvariant)
	}
	decoder := json.NewDecoder(bytes.NewReader(row.Payload))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return zero, fmt.Errorf("%w: source_payload", errLikeVectorInvariant)
	}
	fields := make(map[string]json.RawMessage, 4)
	for decoder.More() {
		token, err := decoder.Token()
		name, valid := token.(string)
		if err != nil || !valid || name != "blogId" && name != "userId" && name != "liked" && name != "version" {
			return zero, fmt.Errorf("%w: source_payload_field", errLikeVectorInvariant)
		}
		if _, duplicate := fields[name]; duplicate {
			return zero, fmt.Errorf("%w: duplicate_source_payload_field", errLikeVectorInvariant)
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) {
			return zero, fmt.Errorf("%w: source_payload_value", errLikeVectorInvariant)
		}
		fields[name] = raw
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') || len(fields) != 4 {
		return zero, fmt.Errorf("%w: source_payload_object", errLikeVectorInvariant)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return zero, fmt.Errorf("%w: extra_source_payload", errLikeVectorInvariant)
	}
	var note, user string
	var liked bool
	var version int64
	if json.Unmarshal(fields["blogId"], &note) != nil || json.Unmarshal(fields["userId"], &user) != nil ||
		json.Unmarshal(fields["liked"], &liked) != nil || json.Unmarshal(fields["version"], &version) != nil ||
		note != key.NoteID || user != key.UserID || version != key.Version || liked != (version%2 == 1) {
		return zero, fmt.Errorf("%w: source_payload_mismatch", errLikeVectorInvariant)
	}
	payload, err := json.Marshal(struct {
		NoteID  string `json:"blogId"`
		UserID  string `json:"userId"`
		Liked   bool   `json:"liked"`
		Version int64  `json:"version"`
	}{note, user, liked, version})
	if err != nil {
		return zero, err
	}
	body, err := json.Marshal(struct {
		EventID       string          `json:"eventId"`
		EventType     string          `json:"eventType"`
		AggregateType string          `json:"aggregateType"`
		AggregateID   string          `json:"aggregateId"`
		Version       int64           `json:"version"`
		Payload       json.RawMessage `json:"payload"`
	}{row.EventID, row.EventType, row.AggregateType, row.AggregateID, version, payload})
	if err != nil {
		return zero, err
	}
	return sha256.Sum256(body), nil
}

func (set *likeVectorReadSet) Evaluate(rows []likeVectorSource) (*likeVectorProposal, error) {
	if set == nil || set.owner == nil || len(rows) != len(set.keys) {
		return nil, fmt.Errorf("%w: incomplete_source_refresh", errLikeVectorInvariant)
	}
	engine := set.owner
	engine.mu.Lock()
	stale := engine.state != set.base
	engine.mu.Unlock()
	if stale {
		return nil, errLikeVectorStale
	}
	next := &likeVectorState{positions: set.positions, pending: make(map[string]likeVectorKey), seen: set.base.seen + set.newEvents, retired: set.base.retired}
	view := likeVectorView{Counts: counts{Events: next.seen, Done: next.retired}, ReadBytesUpperBound: set.readBytes}
	for _, group := range []string{"follow-v1", "feed-v1", "search-v1"} {
		view.Receipts = append(view.Receipts, receipt{Projection: group, Ignored: next.retired})
	}
	seen, sourceIDs := make(map[string]bool, len(rows)), make(map[uint64]bool, len(rows))
	for _, row := range rows {
		key, requested := set.keys[row.EventID]
		if !requested || seen[row.EventID] || sourceIDs[row.ID] {
			return nil, fmt.Errorf("%w: extra_or_duplicate_source", errLikeVectorInvariant)
		}
		seen[row.EventID], sourceIDs[row.ID] = true, true
		hash, err := likeVectorSourceHash(key, row)
		if err != nil {
			return nil, err
		}
		if key.SourceID != 0 && (key.SourceID != row.ID || key.SourceHash != hash) {
			return nil, fmt.Errorf("%w: immutable_source_changed", errLikeVectorInvariant)
		}
		if key.SourceDone && row.DeliveryStatus != "DONE" {
			return nil, fmt.Errorf("%w: done_source_regressed", errLikeVectorInvariant)
		}
		key.SourceID, key.SourceHash = row.ID, hash
		key.SourceDone = row.DeliveryStatus == "DONE"
		switch row.DeliveryStatus {
		case "PENDING":
			view.Counts.Pending++
		case "PROCESSING":
			view.Counts.Processing++
		case "DONE":
			view.Counts.Done++
		default:
			return nil, fmt.Errorf("%w: source_review_or_invalid_status", errLikeVectorInvariant)
		}
		if len(row.Receipts) > 3 {
			return nil, fmt.Errorf("%w: excess_receipts", errLikeVectorInvariant)
		}
		present := [3]bool{}
		var mask uint8
		for _, actual := range row.Receipts {
			index := -1
			for i := range view.Receipts {
				if actual.Projection == view.Receipts[i].Projection {
					index = i
				}
			}
			if index < 0 || present[index] || actual.EventID != key.EventID || actual.Hash != hash || actual.State != "IGNORED" {
				return nil, fmt.Errorf("%w: receipt_identity_hash_or_state", errLikeVectorInvariant)
			}
			present[index] = true
			mask |= 1 << uint(index)
			view.Receipts[index].Ignored++
		}
		if mask&key.ReceiptMask != key.ReceiptMask {
			return nil, fmt.Errorf("%w: immutable_receipt_disappeared", errLikeVectorInvariant)
		}
		key.ReceiptMask = mask
		if row.DeliveryStatus == "DONE" && present == [3]bool{true, true, true} {
			next.retired++
		} else {
			next.pending[key.EventID] = key
			next.trackedBytes += likeVectorKeyBytes(key)
			if int64(len(next.pending)) > engine.limits.MaxPendingEvents || next.trackedBytes > engine.limits.MaxTrackedBytes {
				return nil, fmt.Errorf("%w: unretired_state", errLikeVectorLimit)
			}
		}
	}
	for index := range view.Receipts {
		view.Receipts[index].Missing = next.seen - view.Receipts[index].Ignored
	}
	view.RetiredEvents, view.PendingEvents, view.TrackedBytes = next.retired, int64(len(next.pending)), next.trackedBytes
	return &likeVectorProposal{owner: engine, base: set.base, next: next, view: view}, nil
}

func (proposal *likeVectorProposal) View() likeVectorView {
	if proposal == nil {
		return likeVectorView{}
	}
	view := proposal.view
	view.Receipts = append([]receipt(nil), view.Receipts...)
	return view
}

// The gates are set by the future SQL adapter only after the actual read-only
// commit and complete SQL+Kafka sample succeed. Rejected proposals are consumed;
// retry must read/evaluate fresh evidence, never later relabel a failed sample.
func (engine *likeVectorEngine) Commit(proposal *likeVectorProposal, gate likeVectorCommitGate) error {
	if engine == nil || proposal == nil || proposal.owner != engine {
		return errLikeVectorStale
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if proposal.consumed || engine.state != proposal.base {
		return errLikeVectorStale
	}
	proposal.consumed = true
	if !gate.SQLCommitted || !gate.SampleComplete {
		return errLikeVectorRejected
	}
	if engine.state.generation == ^uint64(0) {
		return fmt.Errorf("%w: generation_overflow", errLikeVectorLimit)
	}
	proposal.next.generation = engine.state.generation + 1
	engine.state = proposal.next
	return nil
}

func (engine *likeVectorEngine) Snapshot() likeVectorSnapshot {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	state := engine.state
	value := likeVectorSnapshot{Generation: state.generation, Positions: make(map[string]likeVectorPosition, len(state.positions)),
		SeenEvents: state.seen, RetiredEvents: state.retired, TrackedBytes: state.trackedBytes}
	for note, position := range state.positions {
		value.Positions[note] = position
	}
	for _, key := range state.pending {
		value.Pending = append(value.Pending, key)
	}
	sort.Slice(value.Pending, func(i, j int) bool { return value.Pending[i].EventID < value.Pending[j].EventID })
	return value
}

// OBS-V2 remains unwired. These bounds apply to one SQL observation, never to
// the separate full-audit budget or a business request/transaction.
const (
	likeVectorSQLChunk      = 256
	likeVectorPayloadBytes  = int64(1 << 20)
	likeVectorSQLValueBytes = int64(256 << 20)
)

type likeVectorSQLStats struct {
	StartedAt, FinishedAt time.Time
	Phase                 string
	ConnectionID          uint64
	SourceCollation       string
	// SourcePrepares counts only explicit reusable source SELECT prepares. The
	// independent driver may additionally prepare parameterized fact queries;
	// this is not the server's total Com_stmt_prepare/execute/close counter.
	Queries, SourcePrepares, FactRows, SourceRows int
	RequestedKeys, PreviousPending                int
	NewEvents, LogicalBytes, SourceValueBytes     int64
	SQLCommitted                                  bool
}

// This narrow interface enables controlled errors around real read-only Tx
// operations without replacing the production driver or using global hooks.
type likeVectorSQLTx interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	Commit() error
	Rollback() error
}

type likeVectorSQLCandidate struct {
	engine       *likeVectorEngine
	proposal     *likeVectorProposal
	view         sqlView
	sqlCommitted bool
}

func (candidate *likeVectorSQLCandidate) SQLView() *sqlView {
	if candidate == nil {
		return nil
	}
	view := candidate.view
	view.Receipts = append([]receipt(nil), view.Receipts...)
	return &view
}

// No SQL adapter may assume Kafka succeeded. The future whole-sample caller
// explicitly supplies that gate; rejection consumes the proposal unchanged.
func (candidate *likeVectorSQLCandidate) Accept(sampleComplete bool) error {
	if candidate == nil {
		return errLikeVectorStale
	}
	return candidate.engine.Commit(candidate.proposal, likeVectorCommitGate{candidate.sqlCommitted, sampleComplete})
}

func likeVectorControlsSQL(ctx context.Context, tx likeVectorSQLTx, stats *likeVectorSQLStats) (likeVectorControls, error) {
	var controls likeVectorControls
	stats.Phase = "controls"
	stats.Queries++
	rows, err := tx.QueryContext(ctx, `SELECT projection,rebuilt_at FROM tb_community_projection_control
 WHERE projection IN ('follow-v1','feed-v1') ORDER BY projection`)
	if err != nil {
		return controls, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var projection string
		var rebuilt sql.NullString
		if err = rows.Scan(&projection, &rebuilt); err != nil {
			_ = rows.Close()
			return controls, err
		}
		index := -1
		if projection == "follow-v1" {
			index = 0
		} else if projection == "feed-v1" {
			index = 1
		}
		if index < 0 || seen[projection] {
			_ = rows.Close()
			return controls, fmt.Errorf("%w: control_rows", errLikeVectorInvariant)
		}
		seen[projection] = true
		body, err := json.Marshal(struct {
			Projection string
			RebuiltAt  sql.NullString
		}{projection, rebuilt})
		if err != nil {
			_ = rows.Close()
			return controls, err
		}
		digest := sha256.Sum256(body)
		controls[index] = hex.EncodeToString(digest[:])
	}
	if err = rows.Err(); err == nil {
		err = rows.Close()
	} else {
		_ = rows.Close()
	}
	if err != nil {
		return controls, err
	}
	if len(seen) != 2 {
		return controls, fmt.Errorf("%w: missing_control", errLikeVectorInvariant)
	}
	stats.Queries++
	var name, alias, generation string
	var rebuilt sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT name,index_alias,active_generation,rebuilt_at
 FROM tb_note_search_control WHERE name='search-v1'`).Scan(&name, &alias, &generation, &rebuilt); err != nil {
		return controls, err
	}
	if name != "search-v1" {
		return controls, fmt.Errorf("%w: search_control", errLikeVectorInvariant)
	}
	body, err := json.Marshal(struct {
		Name, IndexAlias, ActiveGeneration string
		RebuiltAt                          sql.NullString
	}{name, alias, generation, rebuilt})
	if err != nil {
		return controls, err
	}
	digest := sha256.Sum256(body)
	controls[2] = hex.EncodeToString(digest[:])
	return controls, nil
}

func likeVectorFactsQuery(pairs []likeVectorPair) (string, []any, error) {
	if len(pairs) < 1 || len(pairs) > 4096 {
		return "", nil, errLikeVectorInput
	}
	args := make([]any, 0, len(pairs)+1)
	for _, pair := range pairs {
		id, err := strconv.ParseUint(pair.NoteID, 10, 64)
		if err != nil {
			return "", nil, errLikeVectorInput
		}
		args = append(args, id)
	}
	args = append(args, len(pairs)+1)
	query := `SELECT CAST(blog_id AS CHAR),CAST(user_id AS CHAR),version,active
 FROM tb_blog_like FORCE INDEX (uk_blog_like) WHERE blog_id IN (` +
		strings.TrimSuffix(strings.Repeat("?,", len(pairs)), ",") + `) ORDER BY blog_id,user_id LIMIT ?`
	return query, args, nil
}

func likeVectorFactsSQL(ctx context.Context, tx likeVectorSQLTx, engine *likeVectorEngine, stats *likeVectorSQLStats) ([]likeVectorFact, error) {
	stats.Phase = "facts"
	query, args, err := likeVectorFactsQuery(engine.pairs)
	if err != nil {
		return nil, err
	}
	stats.Queries++
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := make([]likeVectorFact, 0, len(engine.pairs))
	for rows.Next() {
		var row likeVectorFact
		if err := rows.Scan(&row.NoteID, &row.UserID, &row.Version, &row.Active); err != nil {
			return nil, err
		}
		stats.FactRows++
		facts = append(facts, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return facts, nil
}

func likeVectorSourceSchema(ctx context.Context, tx likeVectorSQLTx, stats *likeVectorSQLStats) (string, error) {
	stats.Phase = "source_schema"
	stats.Queries++
	var charset, collation string
	if err := tx.QueryRowContext(ctx, `SELECT CHARACTER_SET_NAME,COLLATION_NAME FROM INFORMATION_SCHEMA.COLUMNS
 WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='tb_community_outbox' AND COLUMN_NAME='event_id'`).Scan(&charset, &collation); err != nil {
		return "", err
	}
	stats.SourceCollation = collation
	if charset != "utf8mb4" || collation != "utf8mb4_0900_ai_ci" {
		return "", fmt.Errorf("%w: source_key_collation", errLikeVectorInvariant)
	}
	return collation, nil
}

func likeVectorSourceQuery(size int, collation string) (string, error) {
	// The reviewed whitelist is checked from actual I_S metadata on every
	// nonempty sample; arbitrary server/config values never enter SQL text.
	if size < 1 || size > likeVectorSQLChunk || collation != "utf8mb4_0900_ai_ci" {
		return "", errLikeVectorInput
	}
	keys := make([]string, size)
	for index := range keys {
		keys[index] = "SELECT CAST(? AS CHAR CHARACTER SET utf8mb4) COLLATE " + collation + " AS event_id"
	}
	query := `SELECT o.id,o.event_id,o.event_type,o.aggregate_type,o.aggregate_id,o.aggregate_version,
 OCTET_LENGTH(o.payload),CASE WHEN OCTET_LENGTH(o.payload)<=1048576 THEN o.payload ELSE NULL END,o.delivery_status,
 rf.event_id,rf.event_hash,rf.state,rd.event_id,rd.event_hash,rd.state,rs.event_id,rs.event_hash,rs.state
 FROM (` + strings.Join(keys, " UNION ALL ") + ` LIMIT 256) k
 STRAIGHT_JOIN tb_community_outbox o FORCE INDEX (uk_community_event)
 ON o.event_id=k.event_id AND BINARY o.event_id=BINARY k.event_id
 LEFT JOIN tb_community_projection_event rf FORCE INDEX (PRIMARY)
 ON rf.projection='follow-v1' AND BINARY rf.projection=BINARY 'follow-v1' AND rf.event_id=CONVERT(o.event_id USING ascii) COLLATE ascii_bin AND BINARY rf.event_id=BINARY o.event_id
 LEFT JOIN tb_community_projection_event rd FORCE INDEX (PRIMARY)
 ON rd.projection='feed-v1' AND BINARY rd.projection=BINARY 'feed-v1' AND rd.event_id=CONVERT(o.event_id USING ascii) COLLATE ascii_bin AND BINARY rd.event_id=BINARY o.event_id
 LEFT JOIN tb_community_projection_event rs FORCE INDEX (PRIMARY)
 ON rs.projection='search-v1' AND BINARY rs.projection=BINARY 'search-v1' AND rs.event_id=CONVERT(o.event_id USING ascii) COLLATE ascii_bin AND BINARY rs.event_id=BINARY o.event_id`
	return query, nil
}

func likeVectorScanSources(rows *sql.Rows, stats *likeVectorSQLStats, result *[]likeVectorSource) error {
	for rows.Next() {
		var row likeVectorSource
		var payloadLength int64
		var payload sql.RawBytes
		var received [3]struct {
			EventID, State sql.NullString
			Hash           sql.RawBytes
		}
		values := []any{&row.ID, &row.EventID, &row.EventType, &row.AggregateType, &row.AggregateID, &row.Version,
			&payloadLength, &payload, &row.DeliveryStatus}
		for index := range received {
			values = append(values, &received[index].EventID, &received[index].Hash, &received[index].State)
		}
		if err := rows.Scan(values...); err != nil {
			return err
		}
		stats.SourceRows++
		if payloadLength < 0 || payloadLength > likeVectorPayloadBytes {
			return fmt.Errorf("%w: source_payload_bytes", errLikeVectorLimit)
		}
		if int64(len(payload)) != payloadLength || payload == nil {
			return fmt.Errorf("%w: source_payload_length", errLikeVectorInvariant)
		}
		valueBytes := int64(16 + len(payload) + len(row.EventID) + len(row.EventType) + len(row.AggregateType) + len(row.AggregateID) + len(row.DeliveryStatus))
		for _, actual := range received {
			valueBytes += int64(len(actual.EventID.String) + len(actual.State.String) + len(actual.Hash))
		}
		if valueBytes > likeVectorSQLValueBytes-stats.SourceValueBytes {
			return fmt.Errorf("%w: source_result_value_bytes", errLikeVectorLimit)
		}
		stats.SourceValueBytes += valueBytes
		// Check the cumulative bound before retaining large payload arrays.
		row.Payload = append(json.RawMessage(nil), payload...)
		for index, projection := range []string{"follow-v1", "feed-v1", "search-v1"} {
			actual := received[index]
			if !actual.EventID.Valid && !actual.State.Valid && actual.Hash == nil {
				continue
			}
			if !actual.EventID.Valid || !actual.State.Valid || len(actual.Hash) != sha256.Size {
				return fmt.Errorf("%w: partial_receipt_columns", errLikeVectorInvariant)
			}
			r := likeVectorReceipt{Projection: projection, EventID: actual.EventID.String, State: actual.State.String}
			copy(r.Hash[:], actual.Hash)
			row.Receipts = append(row.Receipts, r)
		}
		*result = append(*result, row)
	}
	return rows.Err()
}

func readLikeVectorSQL(ctx context.Context, db *sql.DB, engine *likeVectorEngine, after int64) (*likeVectorSQLCandidate, *likeVectorSQLStats, error) {
	stats := &likeVectorSQLStats{StartedAt: time.Now().UTC(), Phase: "begin"}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if db == nil || engine == nil || after < 0 {
		stats.FinishedAt = time.Now().UTC()
		return nil, stats, errLikeVectorInput
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		stats.FinishedAt = time.Now().UTC()
		return nil, stats, err
	}
	return readLikeVectorSQLTx(ctx, tx, engine, after, stats)
}

func readLikeVectorSQLTx(ctx context.Context, tx likeVectorSQLTx, engine *likeVectorEngine, after int64, stats *likeVectorSQLStats) (*likeVectorSQLCandidate, *likeVectorSQLStats, error) {
	defer func() { stats.FinishedAt = time.Now().UTC() }()
	defer tx.Rollback()
	stats.Phase = "connection_identity"
	stats.Queries++
	if err := tx.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&stats.ConnectionID); err != nil {
		return nil, stats, err
	}
	controls, err := likeVectorControlsSQL(ctx, tx, stats)
	if err != nil {
		return nil, stats, err
	}
	facts, err := likeVectorFactsSQL(ctx, tx, engine, stats)
	if err != nil {
		return nil, stats, err
	}
	stats.Phase = "vector_begin"
	set, err := engine.Begin(facts, controls)
	if err != nil {
		return nil, stats, err
	}
	keys := set.Keys()
	stats.RequestedKeys, stats.PreviousPending, stats.NewEvents, stats.LogicalBytes = len(keys), len(set.base.pending), set.newEvents, set.readBytes
	sources := make([]likeVectorSource, 0, len(keys))
	var collation string
	if len(keys) > 0 {
		collation, err = likeVectorSourceSchema(ctx, tx, stats)
		if err != nil {
			return nil, stats, err
		}
	}
	statements := map[int]*sql.Stmt{}
	defer func() {
		for _, statement := range statements {
			_ = statement.Close()
		}
	}()
	for offset := 0; offset < len(keys); {
		if err := ctx.Err(); err != nil {
			return nil, stats, err
		}
		size := len(keys) - offset
		if size > likeVectorSQLChunk {
			size = likeVectorSQLChunk
		}
		statement := statements[size]
		if statement == nil {
			stats.Phase = "prepare_sources"
			query, err := likeVectorSourceQuery(size, collation)
			if err != nil {
				return nil, stats, err
			}
			stats.SourcePrepares++
			statement, err = tx.PrepareContext(ctx, query)
			if err != nil {
				return nil, stats, err
			}
			statements[size] = statement
		}
		args := make([]any, 0, size)
		for _, key := range keys[offset : offset+size] {
			args = append(args, key.EventID)
		}
		stats.Phase = "source_rows"
		stats.Queries++
		rows, err := statement.QueryContext(ctx, args...)
		if err != nil {
			return nil, stats, err
		}
		readErr := likeVectorScanSources(rows, stats, &sources)
		closeErr := rows.Close()
		if readErr != nil {
			return nil, stats, readErr
		}
		if closeErr != nil {
			return nil, stats, closeErr
		}
		offset += size
	}
	stats.Phase = "close_prepared_sources"
	for size, statement := range statements {
		if err := statement.Close(); err != nil {
			return nil, stats, err
		}
		delete(statements, size)
	}
	stats.Phase = "evaluate"
	proposal, err := set.Evaluate(sources)
	if err != nil {
		return nil, stats, err
	}
	stats.Phase = "global_parked"
	stats.Queries++
	var parked int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE state='PARKED'").Scan(&parked); err != nil {
		return nil, stats, err
	}
	if parked != 0 {
		return nil, stats, fmt.Errorf("%w: global_parked", errLikeVectorInvariant)
	}
	stats.Phase = "diagnostic_max_id"
	stats.Queries++
	var maxID int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM tb_community_outbox").Scan(&maxID); err != nil {
		return nil, stats, err
	}
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
	stats.Phase = "commit"
	if err := tx.Commit(); err != nil {
		return nil, stats, err
	}
	stats.SQLCommitted = true
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
	stats.Phase = "complete"
	view := proposal.View()
	return &likeVectorSQLCandidate{engine: engine, proposal: proposal, sqlCommitted: true,
		view: sqlView{StartedAt: stats.StartedAt, FinishedAt: time.Now().UTC(), AfterWatermark: after,
			MaxOutboxID: maxID, Counts: view.Counts, Receipts: view.Receipts, Parked: parked}}, stats, nil
}

func read(path string, value any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, value)
}
func save(path string, value any) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
func beneath(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Historical manifests without poolSize keep their original 512-note scope.
// Reading a new deployment must never relabel an old window's observation.
func likeNotePool(body []byte) ([]string, int, error) {
	var manifest struct {
		Project  string          `json:"project"`
		Status   string          `json:"status"`
		PoolSize json.RawMessage `json:"poolSize"`
		Users    json.RawMessage `json:"users"`
		Notes    json.RawMessage `json:"multiNotes"`
		Pairs    json.RawMessage `json:"multiRelations"`
		UserIDs  json.RawMessage `json:"userIds"`
		Multi    []string        `json:"multiNoteIds"`
	}
	if json.Unmarshal(body, &manifest) != nil || manifest.Project != "project-full-stack-cap10" || manifest.Status != "READY" {
		return nil, 0, errors.New("public like fixture invalid")
	}
	count := 512
	if len(manifest.PoolSize) > 0 && json.Unmarshal(manifest.PoolSize, &count) != nil {
		return nil, 0, errors.New("fixture pool must be a bounded integer")
	}
	if count < 1 || count > 4096 || string(manifest.PoolSize) == "null" || len(manifest.Multi) != count {
		return nil, 0, errors.New("fixture pool cardinality differs")
	}
	for _, raw := range []json.RawMessage{manifest.Users, manifest.Notes, manifest.Pairs} {
		if len(raw) > 0 {
			var declared int
			if json.Unmarshal(raw, &declared) != nil || declared != count {
				return nil, 0, errors.New("fixture declared counts differ")
			}
		}
	}
	identities := [][]string{manifest.Multi}
	if len(manifest.UserIDs) > 0 {
		var users []string
		if json.Unmarshal(manifest.UserIDs, &users) != nil || len(users) != count {
			return nil, 0, errors.New("fixture user pool differs")
		}
		identities = append(identities, users)
	}
	for _, ids := range identities {
		seen := make(map[string]bool, count)
		idPattern := regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
		for _, id := range ids {
			if !idPattern.MatchString(id) || seen[id] {
				return nil, 0, errors.New("synthetic identity pool invalid")
			}
			seen[id] = true
		}
	}
	return manifest.Multi, count, nil
}

func likeFixturePath(path, root string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil || abs != path || filepath.Base(abs) != "fixture.json" || filepath.Dir(filepath.Dir(abs)) != root || !regexp.MustCompile(`^like-fixture(?:-[a-z0-9-]{1,80})?$`).MatchString(filepath.Base(filepath.Dir(abs))) {
		return "", errors.New("fixture outside its registered public namespace")
	}
	real, err := filepath.EvalSymlinks(abs)
	realRoot, rootErr := filepath.EvalSymlinks(root)
	if err != nil || rootErr != nil || !beneath(real, realRoot) {
		return "", errors.New("fixture resolves outside public evidence")
	}
	return abs, nil
}

func environmentResourceProfile(env map[string]string, root string) (string, error) {
	path := env["CAP10_ENVIRONMENT_METADATA"]
	if filepath.Clean(path) != path || filepath.Dir(path) != root || !regexp.MustCompile(`^environment(?:-[a-f0-9]{12})?\.json$`).MatchString(filepath.Base(path)) {
		return "", errors.New("environment metadata outside its versioned namespace")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("regular owned environment metadata required")
	}
	realPath, pathErr := filepath.EvalSymlinks(path)
	realRoot, rootErr := filepath.EvalSymlinks(root)
	if pathErr != nil || rootErr != nil || realPath != filepath.Join(realRoot, filepath.Base(path)) {
		return "", errors.New("environment metadata resolves outside its original namespace")
	}
	profile, present := env["CAP10_RESOURCE_PROFILE"]
	if !present {
		profile = "baseline"
	}
	if profile != "baseline" && profile != "mysql8-v1" {
		return "", errors.New("unsupported environment resource profile")
	}
	var metadata map[string]json.RawMessage
	if read(path, &metadata) != nil || metadata == nil {
		return "", errors.New("environment metadata unavailable")
	}
	actual := "baseline"
	if len(metadata["resourceProfile"]) > 0 && json.Unmarshal(metadata["resourceProfile"], &actual) != nil {
		return "", errors.New("environment resource profile invalid")
	}
	if string(metadata["resourceProfile"]) == "null" || actual != profile {
		return "", errors.New("private and public environment profiles differ")
	}
	return profile, nil
}

func sqlSnapshot(ctx context.Context, db *sql.DB, after int64, notes []string) (*sqlView, error) {
	view := &sqlView{StartedAt: time.Now().UTC(), AfterWatermark: after}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM tb_community_outbox").Scan(&view.MaxOutboxID); err != nil {
		return nil, err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(notes)), ",")
	filter := "o.id>? AND o.event_type='LIKE_CHANGED' AND o.aggregate_id IN (" + placeholders + ")"
	args := []any{after}
	for _, id := range notes {
		args = append(args, id)
	}
	query := "SELECT COUNT(*),COALESCE(SUM(delivery_status='PENDING'),0),COALESCE(SUM(delivery_status='PROCESSING'),0),COALESCE(SUM(delivery_status='DONE'),0),COALESCE(SUM(delivery_status='NEEDS_REVIEW'),0),COALESCE(SUM(delivery_status NOT IN ('PENDING','PROCESSING','DONE','NEEDS_REVIEW')),0) FROM tb_community_outbox o WHERE " + filter
	if err = tx.QueryRowContext(ctx, query, args...).Scan(&view.Counts.Events, &view.Counts.Pending, &view.Counts.Processing, &view.Counts.Done, &view.Counts.NeedsReview, &view.Counts.Other); err != nil {
		return nil, err
	}
	for _, group := range groups {
		r := receipt{Projection: group}
		values := append([]any{group}, args...)
		// Match the ASCII receipt PK without converting the indexed column.
		// The binary guard rejects lossy conversion and trailing-space aliases.
		query = "SELECT COUNT(o.id)-COUNT(r.event_id),COALESCE(SUM(r.state='APPLIED'),0),COALESCE(SUM(r.state='IGNORED'),0) FROM tb_community_outbox o LEFT JOIN tb_community_projection_event r ON r.projection=? AND r.event_id=CONVERT(o.event_id USING ascii) COLLATE ascii_bin AND BINARY r.event_id=BINARY o.event_id WHERE " + filter
		if err = tx.QueryRowContext(ctx, query, values...).Scan(&r.Missing, &r.Applied, &r.Ignored); err != nil {
			return nil, err
		}
		view.Receipts = append(view.Receipts, r)
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE state='PARKED'").Scan(&view.Parked); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	view.FinishedAt = time.Now().UTC()
	return view, nil
}

func kafkaSnapshot(ctx context.Context, client *kafka.Client, topic string) (*kafkaView, error) {
	view := &kafkaView{StartedAt: time.Now().UTC(), Topic: topic}
	metadata, err := client.Metadata(ctx, &kafka.MetadataRequest{Topics: []string{topic}})
	if err != nil {
		return nil, err
	}
	if len(metadata.Topics) != 1 || metadata.Topics[0].Name != topic || metadata.Topics[0].Error != nil {
		return nil, errors.New("required topic metadata unavailable")
	}
	partitions := []int{}
	for _, p := range metadata.Topics[0].Partitions {
		if p.Error != nil {
			return nil, errors.New("required partition unavailable")
		}
		partitions = append(partitions, p.ID)
	}
	if len(partitions) < 1 || len(partitions) > 32 {
		return nil, errors.New("partition count outside finite observation bound")
	}
	sort.Ints(partitions)
	commits := map[string]map[int]int64{}
	for _, group := range groups {
		name := "community." + group
		response, e := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: name, Topics: map[string][]int{topic: partitions}})
		if e != nil {
			return nil, e
		}
		if response.Error != nil {
			return nil, response.Error
		}
		rows := response.Topics[topic]
		if len(rows) != len(partitions) {
			return nil, errors.New("missing committed offset")
		}
		commits[name] = map[int]int64{}
		for _, row := range rows {
			if row.Error != nil {
				return nil, row.Error
			}
			commits[name][row.Partition] = row.CommittedOffset
		}
	}
	// Fetch ends after commits: sequential observations are still not atomic, but
	// this avoids a new committed offset overtaking an earlier end observation.
	requests := []kafka.OffsetRequest{}
	for _, p := range partitions {
		requests = append(requests, kafka.FirstOffsetOf(p), kafka.LastOffsetOf(p))
	}
	ends, e := client.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{topic: requests}})
	if e != nil {
		return nil, e
	}
	if len(ends.Topics[topic]) != len(partitions) {
		return nil, errors.New("missing log end offset")
	}
	byPartition := map[int]kafka.PartitionOffsets{}
	for _, row := range ends.Topics[topic] {
		if row.Error != nil {
			return nil, row.Error
		}
		byPartition[row.Partition] = row
	}
	for _, group := range groups {
		name := "community." + group
		g := groupView{Group: name, Initialized: true}
		for _, p := range partitions {
			end, ok := byPartition[p]
			if !ok {
				return nil, errors.New("partition end missing")
			}
			committed, ok := commits[name][p]
			if !ok {
				return nil, errors.New("partition commit missing")
			}
			initialized := committed >= 0
			observed := committed
			if !initialized {
				observed = end.FirstOffset
				g.Initialized = false
			}
			if end.FirstOffset < 0 || end.LastOffset < end.FirstOffset || observed < end.FirstOffset || observed > end.LastOffset {
				return nil, errors.New("offset range inconsistent; never clamp missing or negative lag to zero")
			}
			lag := end.LastOffset - observed
			g.Partitions = append(g.Partitions, partitionView{p, end.FirstOffset, end.LastOffset, committed, lag, initialized})
			g.Lag += lag
		}
		view.Groups = append(view.Groups, g)
	}
	view.FinishedAt = time.Now().UTC()
	return view, nil
}

func baseline(view *sqlView, k *kafkaView) bool {
	if view == nil || k == nil || view.Counts.Pending+view.Counts.Processing+view.Counts.NeedsReview+view.Counts.Other != 0 || view.Parked != 0 {
		return false
	}
	for _, r := range view.Receipts {
		if r.Missing != 0 || r.Applied != 0 || r.Ignored != view.Counts.Events {
			return false
		}
	}
	for _, g := range k.Groups {
		if g.Lag != 0 || !g.Initialized {
			return false
		}
	}
	return len(k.Groups) == 3 && len(view.Receipts) == 3
}

func observe(db *sql.DB, client *kafka.Client, topic string, watermark int64, notes []string, start, deadline time.Time) sample {
	return observeWithBudget(db, client, topic, watermark, notes, start, deadline, 4*time.Second)
}

func observeWithBudget(db *sql.DB, client *kafka.Client, topic string, watermark int64, notes []string, start, deadline time.Time, budget time.Duration) sample {
	started := time.Now()
	s := sample{At: started.UTC(), ElapsedSeconds: time.Since(start).Seconds(), Errors: []string{}, Violations: []string{}}
	limit := started.Add(budget)
	if deadline.Before(limit) {
		limit = deadline
	}
	ctx, cancel := context.WithDeadline(context.Background(), limit)
	defer cancel()
	var err error
	s.SQL, err = sqlSnapshot(ctx, db, watermark, notes)
	if err != nil {
		s.Errors = append(s.Errors, "sql-observation:"+fmt.Sprintf("%T", err))
	} else {
		if s.SQL.Parked > 0 {
			s.Violations = append(s.Violations, "new parked consumer failure")
		}
		if s.SQL.Counts.NeedsReview > 0 {
			s.Violations = append(s.Violations, "LIKE outbox entered NEEDS_REVIEW")
		}
		if s.SQL.Counts.Other > 0 {
			s.Violations = append(s.Violations, "LIKE outbox has unknown status")
		}
		for _, receipt := range s.SQL.Receipts {
			if receipt.Applied > 0 {
				s.Violations = append(s.Violations, "LIKE receipt unexpectedly APPLIED: "+receipt.Projection)
			}
		}
	}
	s.Kafka, err = kafkaSnapshot(ctx, client, topic)
	if err != nil {
		s.Errors = append(s.Errors, "kafka-observation:"+fmt.Sprintf("%T", err))
	} else {
		for _, group := range s.Kafka.Groups {
			if !group.Initialized {
				s.Errors = append(s.Errors, "kafka group committed offsets unavailable: "+group.Group)
			}
		}
	}
	s.DurationMS = float64(time.Since(started)) / float64(time.Millisecond)
	return s
}

func mainRun() error {
	environment := flag.String("environment", "", "private owned cap10 environment")
	output := flag.String("output", "", "new stage-downstream directory")
	doneFile := flag.String("done-file", "", "owned control/done.json")
	likeFixture := flag.String("like-fixture", "", "public like fixture; must match private env")
	maxSeconds := flag.Int("max-seconds", 360, "finite wall-time budget; explicitly selected PTS permits 1200 seconds")
	flag.Parse()
	wallLimit := 360
	if os.Getenv("CAP10_LOAD_BACKEND") == "pts" && os.Getenv("CAP_COHORT") == "cap10" {
		wallLimit = 1200
	}
	if *maxSeconds < 1 || *maxSeconds > wallLimit {
		return errors.New("max-seconds exceeds the selected backend's finite lifecycle budget")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return errors.New("repository cwd unavailable")
	}
	batchRoot := filepath.Join(cwd, "docs", "evidence", "2026-11-01-cap10")
	root := batchRoot
	executionPlatform := platform.GOOS
	if executionPlatform == "linux" {
		root = filepath.Join(batchRoot, "linux")
	} else if executionPlatform != "windows" {
		return errors.New("unsupported CAP10 native executor")
	}
	envPath, err := filepath.Abs(*environment)
	if err != nil {
		return errors.New("private env path invalid")
	}
	runtime := filepath.Dir(envPath)
	if filepath.Dir(runtime) != filepath.Dir(cwd) || !regexp.MustCompile(`^\.runtime-fullstack-cap10-[a-f0-9]{12}$`).MatchString(filepath.Base(runtime)) || !strings.HasPrefix(filepath.Base(envPath), "env") || !strings.HasSuffix(envPath, ".private.json") {
		return errors.New("private environment outside fixed owned runtime")
	}
	env := map[string]string{}
	if read(envPath, &env) != nil {
		return errors.New("private environment unreadable")
	}
	if env["CAP10_PLATFORM"] != executionPlatform {
		return errors.New("native platform or evidence metadata differs")
	}
	if _, err := environmentResourceProfile(env, root); err != nil {
		return err
	}
	if executionPlatform == "linux" {
		raw, readErr := os.ReadFile(filepath.Join(batchRoot, "environment-transition.json"))
		if readErr != nil {
			return errors.New("Linux transition unavailable")
		}
		digest := sha256.Sum256(raw)
		var transition struct {
			Version     int    `json:"version"`
			Batch       string `json:"batch"`
			From        string `json:"fromPlatform"`
			To          string `json:"toPlatform"`
			SwitchCount int    `json:"switchCount"`
			Authorized  bool   `json:"authorized"`
			Host        string `json:"hostLabel"`
			Reference   string `json:"userAuthorizationReference"`
		}
		if hex.EncodeToString(digest[:]) != env["CAP10_TRANSITION_SHA256"] || json.Unmarshal(raw, &transition) != nil || transition.Version != 1 || transition.Batch != "CAP-10" || transition.From != "windows" || transition.To != "linux" || transition.SwitchCount != 1 || !transition.Authorized || transition.Host == "" || transition.Reference == "" {
			return errors.New("Linux transition identity differs")
		}
	}
	if env["CAP10_RUNTIME"] != runtime || env["CAP10_EVIDENCE"] != root || env["MYSQL_HOST"] != "127.0.0.1" || env["MYSQL_PORT"] != "3481" || env["MYSQL_DATABASE"] != "project_full_stack_go" || env["KAFKA_BROKERS"] != "127.0.0.1:9401" || env["KAFKA_TOPIC"] != "community.events" {
		return errors.New("fixed cohort environment differs")
	}
	if env["CAP_READ_OBSERVER_USER"] != "cap10_"+strings.TrimPrefix(filepath.Base(runtime), ".runtime-fullstack-cap10-") || env["CAP_READ_OBSERVER_PASSWORD"] == "" {
		return errors.New("owned read observer identity differs")
	}
	fixture := env["CAP_LIKE_FIXTURE"]
	if *likeFixture != "" {
		if *likeFixture != fixture {
			return errors.New("fixture differs from private environment")
		}
		fixture = *likeFixture
	}
	fixture, err = likeFixturePath(fixture, root)
	if err != nil {
		return err
	}
	fixtureBytes, err := os.ReadFile(fixture)
	if err != nil {
		return errors.New("public like fixture unavailable")
	}
	fixtureDigest := sha256.Sum256(fixtureBytes)
	notes, poolSize, err := likeNotePool(fixtureBytes)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(*output)
	if err != nil || !beneath(out, root) {
		return errors.New("new output outside fixed evidence root")
	}
	done, err := filepath.Abs(*doneFile)
	if err != nil || !beneath(done, root) || filepath.Base(done) != "done.json" || !strings.HasSuffix(filepath.Base(filepath.Dir(done)), "-control") {
		return errors.New("done-file outside owned control path")
	}
	if _, err = os.Stat(done); !errors.Is(err, os.ErrNotExist) {
		return errors.New("preserve existing done-file or unavailable control path")
	}
	if err = os.Mkdir(out, 0700); err != nil {
		return errors.New("output already exists or cannot be created")
	}
	config := mysql.NewConfig()
	config.User = env["CAP_READ_OBSERVER_USER"]
	config.Passwd = env["CAP_READ_OBSERVER_PASSWORD"]
	config.Net = "tcp"
	config.Addr = "127.0.0.1:3481"
	config.DBName = env["MYSQL_DATABASE"]
	config.Timeout = 2 * time.Second
	config.ReadTimeout = 3 * time.Second
	config.WriteTimeout = 2 * time.Second
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return errors.New("observer connection setup failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// Historical admission reads all retained events and receipts once, before
	// any load starts. It must not inherit the bounded in-load sampling budget.
	initialConfig := *config
	initialConfig.ReadTimeout = 15 * time.Second
	initialDB, err := sql.Open("mysql", initialConfig.FormatDSN())
	if err != nil {
		return errors.New("initial observer connection setup failed")
	}
	defer initialDB.Close()
	initialDB.SetMaxOpenConns(1)
	initialDB.SetMaxIdleConns(1)
	transport := &kafka.Transport{DialTimeout: 2 * time.Second, IdleTimeout: time.Second, MetadataTTL: time.Second}
	defer transport.CloseIdleConnections()
	client := &kafka.Client{Addr: kafka.TCP("127.0.0.1:9401"), Timeout: 3 * time.Second, Transport: transport}
	start := time.Now()
	deadline := start.Add(time.Duration(*maxSeconds) * time.Second)
	var initial sample
	ready := false
	startupAttempts := []sample{}
	preflightDeadline := start.Add(75 * time.Second)
	if deadline.Before(preflightDeadline) {
		preflightDeadline = deadline
	}
	for {
		initial = observeWithBudget(initialDB, client, "community.events", 0, notes, start, preflightDeadline, 30*time.Second)
		startupAttempts = append(startupAttempts, initial)
		ready = len(initial.Errors) == 0 && baseline(initial.SQL, initial.Kafka)
		if ready || time.Now().Add(5*time.Second).After(preflightDeadline) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	_ = initialDB.Close()
	if err = save(filepath.Join(out, "ready.json"), map[string]any{"at": time.Now().UTC(), "ready": ready, "baseline": initial, "startupAttempts": startupAttempts, "observerPid": os.Getpid(), "fixedProject": "project-full-stack-cap10", "noteCount": len(notes), "poolSize": poolSize, "fixtureSHA256": hex.EncodeToString(fixtureDigest[:]), "initialBudgetSeconds": 30, "initialReadTimeoutSeconds": 15, "loadBudgetSeconds": 4, "loadReadTimeoutSeconds": 3, "scope": "bounded pre-load full-history readiness; initial cumulative LIKE baseline requires no pre-existing pending/receipt/park/lag before load. In-load observation cadence and budgets are unchanged"}); err != nil {
		return errors.New("ready evidence write failed")
	}
	if !ready {
		return errors.New("initial downstream baseline is not complete and clean; do not start load")
	}
	watermark := initial.SQL.MaxOutboxID
	file, err := os.OpenFile(filepath.Join(out, "timeline.ndjson"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("timeline creation failed")
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	next := start
	samples, failures, consecutive, semanticFailures := 0, 0, 0, 0
	doneObserved := time.Time{}
	recovered := time.Time{}
	complete := false
	for time.Since(start) < time.Duration(*maxSeconds)*time.Second {
		if delay := time.Until(next); delay > 0 {
			if time.Now().Add(delay).After(deadline) {
				break
			}
			time.Sleep(delay)
		}
		s := observe(db, client, "community.events", watermark, notes, start, deadline)
		samples++
		if len(s.Errors) > 0 {
			failures++
			consecutive++
		} else {
			consecutive = 0
		}
		if err = encoder.Encode(s); err != nil {
			return errors.New("timeline write failed")
		}
		if err = file.Sync(); err != nil {
			return errors.New("timeline flush failed")
		}
		if len(s.Violations) > 0 {
			semanticFailures += len(s.Violations)
			_ = save(filepath.Join(filepath.Dir(done), "stop.json"), map[string]any{"at": time.Now().UTC(), "reason": "downstream semantic violation", "violations": s.Violations, "observerPid": os.Getpid()})
		}
		if consecutive >= 3 {
			_ = save(filepath.Join(filepath.Dir(done), "stop.json"), map[string]any{"at": time.Now().UTC(), "reason": "three consecutive incomplete downstream observations", "observerPid": os.Getpid()})
		}
		if _, e := os.Stat(done); e == nil && doneObserved.IsZero() {
			doneObserved = time.Now().UTC()
		}
		if !doneObserved.IsZero() {
			if len(s.Errors) == 0 && baseline(s.SQL, s.Kafka) {
				recovered = time.Now().UTC()
				complete = true
				break
			}
			if time.Since(doneObserved) >= 60*time.Second {
				break
			}
		}
		next = next.Add(5 * time.Second)
		if time.Now().After(next) {
			next = time.Now().Add(5 * time.Second)
		}
	}
	valid := complete && failures == 0 && semanticFailures == 0
	if !complete && doneObserved.IsZero() {
		_ = save(filepath.Join(filepath.Dir(done), "stop.json"), map[string]any{"at": time.Now().UTC(), "reason": "finite downstream observation window ended without owned control completion", "observerPid": os.Getpid()})
	}
	if err = save(filepath.Join(out, "summary.json"), map[string]any{"at": time.Now().UTC(), "valid": valid, "validScope": "complete stage collection, no semantic violations and post-control-done convergence only", "collectionValid": failures == 0, "semanticViolationCount": semanticFailures, "postControlDoneConverged": complete, "nativeRecoveryBudgetVerified": false, "fullChainStableBudgetVerified": false, "samples": samples, "failedSamples": failures, "maxSeconds": *maxSeconds, "startedAt": start.UTC(), "doneObservedAt": nullableTime(doneObserved), "recoveredAt": nullableTime(recovered), "recoveryFromObservedDoneMs": elapsedOrNil(doneObserved, recovered), "startOutboxWatermark": watermark, "baseline": initial, "allSamplesComplete": failures == 0, "scope": "stage samples read only id>startOutboxWatermark; cumulative cohort counts are the clean baseline plus stage counts. Kafka lag is whole topic, three independent groups, fetched sequentially. Final convergence is not steady-state proof. Grade the 60s native recovery budget from native stableEndedAt and timeline, not delayed helper/control completion."}); err != nil {
		return errors.New("summary write failed")
	}
	if !valid {
		return errors.New("downstream observation incomplete or recovery budget not met")
	}
	return nil
}
func fileSHA(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func elapsedOrNil(a, b time.Time) any {
	if a.IsZero() || b.IsZero() {
		return nil
	}
	return float64(b.Sub(a)) / float64(time.Millisecond)
}
func main() {
	if err := mainRun(); err != nil {
		fmt.Fprintln(os.Stderr, "cap10-downstream:", err.Error())
		os.Exit(1)
	}
}
