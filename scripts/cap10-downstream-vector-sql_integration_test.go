package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// No global/reset SQL is used. Native execution requires an explicitly
// registered, empty CAP10 integration database before any fixture is written.
type vectorNativeFixture struct {
	db       *sql.DB
	ctx      context.Context
	pairs    []likeVectorPair
	controls likeVectorControls
	events   []string
	oracle   map[string]vectorNativeOracleEvent
}

type vectorNativeOracleEvent struct {
	NoteID, UserID, EventID, Hash string
	Version                       int64
	Payload                       json.RawMessage
}
type vectorNativeOracle struct {
	Version               int
	Producer, CodecSHA256 string
	Pairs                 []likeVectorPair
	Events                []vectorNativeOracleEvent
}

// Large canonical fixtures consume values produced by the actual production
// Decode through a private overlay. The function under test is not its oracle.
func vectorNativeProductionOracle(t *testing.T) ([]likeVectorPair, map[string]vectorNativeOracleEvent) {
	t.Helper()
	path := os.Getenv("CAP10_VECTOR_PRODUCTION_ORACLE")
	expected := os.Getenv("CAP10_VECTOR_PRODUCTION_ORACLE_SHA256")
	if !filepath.IsAbs(path) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(expected) {
		t.Fatal("exact production Decode oracle path/SHA required for canonical interval")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		t.Fatal("bounded regular production oracle file required")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != expected {
		t.Fatal("production oracle bytes changed")
	}
	var oracle vectorNativeOracle
	if err = json.Unmarshal(body, &oracle); err != nil || oracle.Version != 1 || oracle.Producer != "communityprojection.Decode" || len(oracle.Pairs) != 4096 || len(oracle.Events) != 32768 {
		t.Fatal("canonical oracle identity/cardinality invalid")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source location unavailable")
	}
	codec, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "backend", "internal", "communityprojection", "event.go"))
	if err != nil {
		t.Fatal(err)
	}
	codecDigest := sha256.Sum256(codec)
	if hex.EncodeToString(codecDigest[:]) != oracle.CodecSHA256 {
		t.Fatal("oracle production codec differs from current source")
	}
	known := map[string]likeVectorPair{}
	for _, pair := range oracle.Pairs {
		if _, exists := known[pair.NoteID]; exists {
			t.Fatal("duplicate canonical oracle pair")
		}
		known[pair.NoteID] = pair
	}
	events := map[string]vectorNativeOracleEvent{}
	for _, event := range oracle.Events {
		pair, exists := known[event.NoteID]
		hash, err := hex.DecodeString(event.Hash)
		if !exists || pair.UserID != event.UserID || event.Version < 1 || event.Version > 8 || event.EventID != likeVectorEventID(pair, event.Version) || err != nil || len(hash) != 32 || len(event.Payload) > 1024 {
			t.Fatal("canonical oracle event invalid")
		}
		if _, exists := events[event.EventID]; exists {
			t.Fatal("duplicate canonical oracle event")
		}
		events[event.EventID] = event
	}
	t.Logf("production oracle SHA=%s codecSHA=%s events=%d", expected, oracle.CodecSHA256, len(events))
	return oracle.Pairs, events
}

func vectorNativeRequired(t *testing.T) {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("registered independent MySQL required")
	}
	name := os.Getenv("MYSQL_DATABASE")
	if !regexp.MustCompile(`^cap10_relay_it_[a-f0-9]{12}$`).MatchString(name) {
		t.Skip("registered independent empty database required before connecting")
	}
}

func vectorNative(t *testing.T, count int) *vectorNativeFixture {
	t.Helper()
	vectorNativeRequired(t)
	name := os.Getenv("MYSQL_DATABASE")
	if count < 1 || count > 4096 {
		t.Fatal("fixture pair count outside finite bound")
	}
	port, err := strconv.Atoi(os.Getenv("MYSQL_PORT"))
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("isolated MySQL port invalid")
	}
	cfg := mysql.NewConfig()
	cfg.User = os.Getenv("MYSQL_USER")
	cfg.Passwd = os.Getenv("MYSQL_PASSWORD")
	cfg.DBName = name
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(os.Getenv("MYSQL_HOST"), strconv.Itoa(port))
	cfg.Timeout = 2 * time.Second
	cfg.ReadTimeout = 3 * time.Second
	cfg.WriteTimeout = 3 * time.Second
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("isolated SQL open failed")
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	for _, table := range []string{"tb_blog_like", "tb_community_outbox", "tb_community_projection_event", "tb_community_consumer_failure"} {
		var rows int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("native vector fixture requires empty registered tables; existing rows untouched")
		}
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	base := uint64(8000000000000000) + binary.BigEndian.Uint64(nonce[:])%1000000000000
	f := &vectorNativeFixture{db: db, ctx: ctx}
	if count == 4096 {
		f.pairs, f.oracle = vectorNativeProductionOracle(t)
	} else {
		for i := 0; i < count; i++ {
			f.pairs = append(f.pairs, likeVectorPair{strconv.FormatUint(base+uint64(i), 10), strconv.FormatUint(base+100000+uint64(i), 10)})
		}
	}
	// Record exact ownership before the first INSERT. Cleanup uses only these
	// note/user identities and separately registered event IDs, never a wildcard.
	t.Logf("owned vector fixture database=%s pairs=%d firstNote=%s lastNote=%s", name, count, f.pairs[0].NoteID, f.pairs[count-1].NoteID)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		for start := 0; start < len(f.events); start += 256 {
			end := start + 256
			if end > len(f.events) {
				end = len(f.events)
			}
			args := make([]any, end-start)
			for i, id := range f.events[start:end] {
				args[i] = id
			}
			marks := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
			for _, table := range []string{"tb_community_projection_event", "tb_community_outbox"} {
				if _, err := db.ExecContext(cleanup, "DELETE FROM "+table+" WHERE event_id IN ("+marks+")", args...); err != nil {
					t.Error("exact owned source/receipt cleanup failed")
				}
			}
		}
		for start := 0; start < len(f.pairs); start += 256 {
			end := start + 256
			if end > len(f.pairs) {
				end = len(f.pairs)
			}
			var marks []string
			var args []any
			for _, pair := range f.pairs[start:end] {
				marks = append(marks, "(?,?)", "(?,?)")
				args = append(args, pair.NoteID, pair.UserID, pair.NoteID, pair.UserID+"1")
			}
			if _, err := db.ExecContext(cleanup, "DELETE FROM tb_blog_like WHERE (blog_id,user_id) IN ("+strings.Join(marks, ",")+")", args...); err != nil {
				t.Error("exact owned fact cleanup failed")
			}
		}
	})
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	f.controls, err = likeVectorControlsSQL(ctx, tx, &likeVectorSQLStats{})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *vectorNativeFixture) engine(t *testing.T) *likeVectorEngine {
	t.Helper()
	engine, err := newLikeVector(f.pairs, nil, f.controls, likeVectorLimits{MaxNewEvents: 40000, MaxPendingEvents: 40000, MaxTrackedBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

type vectorNativeExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (f *vectorNativeFixture) insert(t *testing.T, exec vectorNativeExecutor, pair likeVectorPair, version int64, status string, receipts int) uint64 {
	t.Helper()
	key := likeVectorKey{EventID: likeVectorEventID(pair, version), NoteID: pair.NoteID, UserID: pair.UserID, Version: version}
	row := vectorTestRow(t, key, 1, status, receipts)
	f.events = append(f.events, key.EventID)
	result, err := exec.ExecContext(f.ctx, `INSERT INTO tb_community_outbox(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload,delivery_status) VALUES (?,'BLOG',?,?,'LIKE_CHANGED',?,?)`, key.EventID, pair.NoteID, version, string(row.Payload), status)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exec.ExecContext(f.ctx, `INSERT INTO tb_blog_like(blog_id,user_id,active,version) VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE active=VALUES(active),version=VALUES(version)`, pair.NoteID, pair.UserID, version%2, version); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range row.Receipts {
		if _, err = exec.ExecContext(f.ctx, `INSERT INTO tb_community_projection_event(projection,event_id,event_hash,state) VALUES (?,?,?,'IGNORED')`, receipt.Projection, receipt.EventID, receipt.Hash[:]); err != nil {
			t.Fatal(err)
		}
	}
	return uint64(id)
}
func (f *vectorNativeFixture) observe(t *testing.T, engine *likeVectorEngine) *sqlView {
	t.Helper()
	candidate, stats, err := readLikeVectorSQL(f.ctx, f.db, engine, 0)
	if err != nil {
		t.Fatalf("native vector read: phase=%s queries=%d prepares=%d error=%v", stats.Phase, stats.Queries, stats.SourcePrepares, err)
	}
	if err = candidate.Accept(true); err != nil {
		t.Fatal(err)
	}
	return candidate.SQLView()
}
func (f *vectorNativeFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationLikeVectorSQLLowerIDLateCommit(t *testing.T) {
	f := vectorNative(t, 2)
	engine := f.engine(t)
	lower, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lower.Rollback()
	lowerID := f.insert(t, lower, f.pairs[0], 1, "DONE", 3)
	higherID := f.insert(t, f.db, f.pairs[1], 1, "DONE", 3)
	if lowerID >= higherID {
		t.Fatal("fixture did not allocate delayed lower source ID")
	}
	first := f.observe(t, engine)
	if first.Counts.Events != 1 || first.Counts.Done != 1 {
		t.Fatal("uncommitted lower source leaked into RR view")
	}
	if err = lower.Commit(); err != nil {
		t.Fatal(err)
	}
	second := f.observe(t, engine)
	if second.Counts.Events != 2 || second.Counts.Done != 2 || second.Receipts[2].Ignored != 2 {
		t.Fatal("late lower source ID skipped or counted twice")
	}
	full, err := sqlSnapshot(f.ctx, f.db, 0, []string{f.pairs[0].NoteID, f.pairs[1].NoteID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full.Counts, second.Counts) || !reflect.DeepEqual(full.Receipts, second.Receipts) {
		t.Fatal("quiescent vector and full observer disagree")
	}
}

func TestIntegrationLikeVectorSQLPendingAndRepeatableRead(t *testing.T) {
	f := vectorNative(t, 1)
	engine := f.engine(t)
	pair := f.pairs[0]
	f.insert(t, f.db, pair, 1, "PENDING", 2)
	first := f.observe(t, engine)
	if first.Counts.Pending != 1 || first.Receipts[2].Missing != 1 {
		t.Fatal("pending or missing receipt concealed")
	}
	id := likeVectorEventID(pair, 1)
	f.exec(t, `UPDATE tb_community_outbox SET delivery_status='PROCESSING' WHERE event_id=?`, id)
	if view := f.observe(t, engine); view.Counts.Processing != 1 || view.Receipts[2].Missing != 1 {
		t.Fatal("unchanged version did not refresh nonterminal processing source")
	}
	f.exec(t, `UPDATE tb_community_outbox SET delivery_status='PENDING' WHERE event_id=?`, id)
	if view := f.observe(t, engine); view.Counts.Pending != 1 {
		t.Fatal("retry transition did not refresh pending source")
	}
	tx, err := f.db.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = likeVectorControlsSQL(f.ctx, tx, &likeVectorSQLStats{}); err != nil {
		t.Fatal(err)
	} // Establish the RR view before the other connection commits.
	key := likeVectorKey{EventID: id, NoteID: pair.NoteID, UserID: pair.UserID, Version: 1}
	row := vectorTestRow(t, key, 1, "DONE", 3)
	f.exec(t, `UPDATE tb_community_outbox SET delivery_status='DONE' WHERE event_id=?`, id)
	f.exec(t, `INSERT INTO tb_community_projection_event(projection,event_id,event_hash,state) VALUES ('search-v1',?,?,'IGNORED')`, id, row.Receipts[2].Hash[:])
	candidate, stats, err := readLikeVectorSQLTx(f.ctx, tx, engine, 0, &likeVectorSQLStats{StartedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if !stats.SQLCommitted || candidate.SQLView().Counts.Pending != 1 || candidate.SQLView().Receipts[2].Missing != 1 {
		t.Fatal("read-only RR mixed post-snapshot receipt/DONE writes")
	}
	if err = candidate.Accept(true); err != nil {
		t.Fatal(err)
	}
	last := f.observe(t, engine)
	if last.Counts.Done != 1 || last.Receipts[2].Ignored != 1 || engine.Snapshot().RetiredEvents != 1 {
		t.Fatal("pending key not fully refreshed and retired")
	}
	if repeat := f.observe(t, engine); !reflect.DeepEqual(repeat.Counts, last.Counts) || engine.Snapshot().RetiredEvents != 1 {
		t.Fatal("terminal event counted repeatedly")
	}
}

func TestIntegrationLikeVectorSQLRejectsCorruption(t *testing.T) {
	vectorNativeRequired(t)
	for _, kind := range []string{"extra-user", "removed-fact", "regressed-version", "source-alias", "missing-source", "receipt-hash", "receipt-state", "receipt-alias", "projection-alias"} {
		t.Run(kind, func(t *testing.T) {
			f := vectorNative(t, 1)
			pair := f.pairs[0]
			engine := f.engine(t)
			f.insert(t, f.db, pair, 1, "PENDING", 2)
			f.observe(t, engine)
			before := engine.Snapshot()
			id := likeVectorEventID(pair, 1)
			switch kind {
			case "extra-user":
				f.exec(t, `INSERT INTO tb_blog_like(blog_id,user_id,active,version) VALUES (?,?,1,1)`, pair.NoteID, pair.UserID+"1")
			case "removed-fact":
				f.exec(t, `DELETE FROM tb_blog_like WHERE blog_id=? AND user_id=?`, pair.NoteID, pair.UserID)
			case "regressed-version":
				f.exec(t, `UPDATE tb_blog_like SET version=0,active=0 WHERE blog_id=? AND user_id=?`, pair.NoteID, pair.UserID)
			case "source-alias":
				alias := strings.ToLower(id)
				f.events = append(f.events, alias)
				f.exec(t, `UPDATE tb_community_outbox SET event_id=? WHERE event_id=?`, alias, id)
			case "missing-source":
				f.exec(t, `DELETE FROM tb_community_outbox WHERE event_id=?`, id)
			case "receipt-hash":
				f.exec(t, `UPDATE tb_community_projection_event SET event_hash=? WHERE projection='follow-v1' AND event_id=?`, make([]byte, 32), id)
			case "receipt-state":
				f.exec(t, `UPDATE tb_community_projection_event SET state='APPLIED' WHERE projection='follow-v1' AND event_id=?`, id)
			case "receipt-alias":
				alias := id + " "
				f.events = append(f.events, alias)
				f.exec(t, `UPDATE tb_community_projection_event SET event_id=? WHERE projection='follow-v1' AND event_id=?`, alias, id)
			case "projection-alias":
				f.exec(t, `UPDATE tb_community_projection_event SET projection='follow-v1 ' WHERE projection='follow-v1' AND event_id=?`, id)
			}
			candidate, _, err := readLikeVectorSQL(f.ctx, f.db, engine, 0)
			if err == nil || candidate != nil {
				t.Fatal("invalid SQL facts/source/receipt yielded candidate")
			}
			if !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("invalid SQL advanced engine")
			}
		})
	}
}

type vectorNativeFaultTx struct {
	*sql.Tx
	commitErr error
	cancel    context.CancelFunc
}

func (tx vectorNativeFaultTx) Commit() error {
	if tx.commitErr != nil {
		_ = tx.Tx.Rollback()
		return tx.commitErr
	}
	return tx.Tx.Commit()
}
func (tx vectorNativeFaultTx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	if tx.cancel != nil {
		tx.cancel()
	}
	return tx.Tx.PrepareContext(ctx, query)
}
func TestIntegrationLikeVectorSQLCommitCancelAndKafkaGate(t *testing.T) {
	vectorNativeRequired(t)
	for _, kind := range []string{"commit", "cancel", "kafka"} {
		t.Run(kind, func(t *testing.T) {
			f := vectorNative(t, 1)
			f.insert(t, f.db, f.pairs[0], 1, "DONE", 3)
			engine := f.engine(t)
			before := engine.Snapshot()
			ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
			defer cancel()
			tx, err := f.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fault := vectorNativeFaultTx{Tx: tx}
			sentinel := errors.New("injected lost read-only COMMIT response")
			if kind == "commit" {
				fault.commitErr = sentinel
			}
			if kind == "cancel" {
				fault.cancel = cancel
			}
			candidate, _, err := readLikeVectorSQLTx(ctx, fault, engine, 0, &likeVectorSQLStats{StartedAt: time.Now().UTC()})
			if kind == "kafka" {
				if err != nil {
					t.Fatal(err)
				}
				if err = candidate.Accept(false); !errors.Is(err, errLikeVectorRejected) {
					t.Fatal("Kafka rejection bypassed")
				}
				if err = candidate.Accept(true); !errors.Is(err, errLikeVectorStale) {
					t.Fatal("failed sample relabeled")
				}
			} else if err == nil || candidate != nil {
				t.Fatal("SQL fault returned candidate")
			}
			if !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("failed SQL/Kafka published")
			}
		})
	}
}

func vectorNativePlanTables(value any, result *[]map[string]any) {
	switch node := value.(type) {
	case map[string]any:
		if table, ok := node["table"].(map[string]any); ok {
			*result = append(*result, table)
		}
		for _, child := range node {
			vectorNativePlanTables(child, result)
		}
	case []any:
		for _, child := range node {
			vectorNativePlanTables(child, result)
		}
	}
}
func TestIntegrationLikeVectorSQLPointPlans(t *testing.T) {
	f := vectorNative(t, 1)
	f.insert(t, f.db, f.pairs[0], 1, "DONE", 3)
	vectorNativeAssertPointPlans(t, f, "small-owned-density")
}

func vectorNativeAssertPointPlans(t *testing.T, f *vectorNativeFixture, density string) {
	t.Helper()
	factQuery, factArgs, err := likeVectorFactsQuery(f.pairs)
	if err != nil {
		t.Fatal(err)
	}
	var factPlan []byte
	if err = f.db.QueryRowContext(f.ctx, "EXPLAIN FORMAT=JSON "+factQuery, factArgs...).Scan(&factPlan); err != nil {
		t.Fatal(err)
	}
	// Record density rather than asserting an arbitrary IN-list never scans.
	// This fixture cannot certify access at the untouched 1.2M history density.
	var totalFacts int64
	if err = f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM tb_blog_like").Scan(&totalFacts); err != nil {
		t.Fatal(err)
	}
	t.Logf("fact-plan density=%s totalFactRows=%d selectedNotes=%d hardRowLimit=%d raw=%s", density, totalFacts, len(f.pairs), len(f.pairs)+1, factPlan)
	id := likeVectorEventID(f.pairs[0], 1)
	var charset, collation string
	if err = f.db.QueryRowContext(f.ctx, `SELECT CHARACTER_SET_NAME,COLLATION_NAME FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='tb_community_outbox' AND COLUMN_NAME='event_id'`).Scan(&charset, &collation); err != nil || charset != "utf8mb4" || collation != "utf8mb4_0900_ai_ci" {
		t.Fatal("actual source key schema differs from reviewed driver collation")
	}
	t.Logf("source key charset=%s collation=%s driver charset=utf8mb4 collation=%s", charset, collation, collation)
	for _, size := range []int{1, 256} {
		query, err := likeVectorSourceQuery(size, collation)
		if err != nil {
			t.Fatal(err)
		}
		args := make([]any, 0, size)
		for i := 0; i < size; i++ {
			key := id
			if len(f.pairs) >= 32 {
				key = likeVectorEventID(f.pairs[i/8], int64(i%8+1))
			}
			args = append(args, key)
		}
		var raw []byte
		if err = f.db.QueryRowContext(f.ctx, "EXPLAIN FORMAT=JSON "+query, args...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plan any
		if err = json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		var tables []map[string]any
		vectorNativePlanTables(plan, &tables)
		counts := map[string]int{}
		for _, table := range tables {
			alias, _ := table["table_name"].(string)
			if alias == "k" {
				counts[alias]++
				access, _ := table["access_type"].(string)
				scanned, ok := table["rows_examined_per_scan"].(float64)
				if !ok || scanned > float64(size) || access != "ALL" && access != "system" {
					t.Fatalf("key driver not bounded materialized rows: %s", raw)
				}
				if _, ok := table["materialized_from_subquery"]; !ok {
					t.Fatalf("key driver was merged: %s", raw)
				}
				continue
			}
			if alias != "o" && alias != "rf" && alias != "rd" && alias != "rs" {
				continue
			}
			counts[alias]++
			key, _ := table["key"].(string)
			access, _ := table["access_type"].(string)
			parts, _ := table["used_key_parts"].([]any)
			scanned, ok := table["rows_examined_per_scan"].(float64)
			if !ok || scanned != 1 {
				t.Fatalf("target point lookup does not scan one row: %s", raw)
			}
			if alias == "o" {
				if key != "uk_community_event" || access != "const" && access != "eq_ref" && access != "ref" || len(parts) != 1 {
					t.Fatalf("source is not unique-key point access: %s", raw)
				}
			} else {
				if key != "PRIMARY" || access != "eq_ref" && access != "const" || len(parts) != 2 {
					t.Fatalf("receipt is not complete PK point access: %s", raw)
				}
			}
		}
		for _, alias := range []string{"o", "rf", "rd", "rs"} {
			if counts[alias] != 1 {
				t.Fatalf("plan omitted expected point branches %s: %s", alias, raw)
			}
		}
		if counts["k"] != 1 {
			t.Fatalf("bounded driver missing: %s", raw)
		}
		t.Logf("point-plan density=%s branches=%d sourceUK=%d receiptPK=%d raw=%s", density, size, counts["o"], counts["rf"]+counts["rd"]+counts["rs"], raw)
	}
}

// One explicitly enabled, modest canonical interval tests the full SQL adapter
// at 4096 facts/32768 events/98304 receipts. This is SQL-only, never API QPS.
func TestIntegrationLikeVectorSQLCanonicalInterval(t *testing.T) {
	if os.Getenv("CAP10_VECTOR_CANONICAL_INTERVAL") != "1" {
		t.Skip("explicit modest interval authorization required")
	}
	f := vectorNative(t, 4096)
	engine := f.engine(t)
	seedStart := time.Now()
	for start := 0; start < len(f.pairs); start += 32 {
		var sourceMarks, factMarks, receiptMarks []string
		var sourceArgs, factArgs, receiptArgs []any
		for _, pair := range f.pairs[start : start+32] {
			factMarks = append(factMarks, "(?,?,0,8)")
			factArgs = append(factArgs, pair.NoteID, pair.UserID)
			for version := int64(1); version <= 8; version++ {
				key := likeVectorKey{EventID: likeVectorEventID(pair, version), NoteID: pair.NoteID, UserID: pair.UserID, Version: version}
				row := vectorTestRow(t, key, 1, "DONE", 3)
				oracle, exists := f.oracle[key.EventID]
				if !exists {
					t.Fatal("production oracle event missing")
				}
				hash, err := hex.DecodeString(oracle.Hash)
				if err != nil {
					t.Fatal(err)
				}
				row.Payload = append(json.RawMessage(nil), oracle.Payload...)
				for index := range row.Receipts {
					copy(row.Receipts[index].Hash[:], hash)
				}
				f.events = append(f.events, key.EventID)
				sourceMarks = append(sourceMarks, "(?,'BLOG',?,?,'LIKE_CHANGED',?,'DONE')")
				sourceArgs = append(sourceArgs, key.EventID, pair.NoteID, version, string(row.Payload))
				for _, receipt := range row.Receipts {
					receiptMarks = append(receiptMarks, "(?,?,?,'IGNORED')")
					receiptArgs = append(receiptArgs, receipt.Projection, receipt.EventID, receipt.Hash[:])
				}
			}
		}
		tx, err := f.db.BeginTx(f.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer tx.Rollback()
			for _, statement := range []struct {
				query string
				args  []any
			}{
				{`INSERT INTO tb_community_outbox(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload,delivery_status) VALUES ` + strings.Join(sourceMarks, ","), sourceArgs},
				{`INSERT INTO tb_blog_like(blog_id,user_id,active,version) VALUES ` + strings.Join(factMarks, ","), factArgs},
				{`INSERT INTO tb_community_projection_event(projection,event_id,event_hash,state) VALUES ` + strings.Join(receiptMarks, ","), receiptArgs},
			} {
				if _, err = tx.ExecContext(f.ctx, statement.query, statement.args...); err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}()
	}
	t.Logf("canonical seed facts=4096 outbox=32768 receipts=98304 elapsed=%s", time.Since(seedStart))
	vectorNativeAssertPointPlans(t, f, "owned-32768-events-98304-receipts")
	start := time.Now()
	candidate, stats, err := readLikeVectorSQL(f.ctx, f.db, engine, 0)
	metadata, _ := json.Marshal(stats)
	t.Logf("SQL-only canonical adapter elapsed=%s diagnostics=%s", time.Since(start), metadata)
	if err != nil {
		t.Fatalf("canonical SQL adapter failed (no capacity result): %v", err)
	}
	if candidate.SQLView().Counts.Events != 32768 || candidate.SQLView().Receipts[2].Ignored != 32768 || stats.SourcePrepares != 1 || stats.SourceRows != 32768 {
		t.Fatal("canonical interval exact accounting/statement reuse mismatch")
	}
	if err = candidate.Accept(true); err != nil {
		t.Fatal(err)
	}
}
