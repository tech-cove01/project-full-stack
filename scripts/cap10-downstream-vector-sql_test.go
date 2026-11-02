package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// A standard-library-only driver supplies bounded rows and protocol failures.
// It never dials a server. Real MySQL plan/RR tests are a separate gated file.
type vectorSQLMemory struct {
	mu                     sync.Mutex
	facts                  []likeVectorFact
	sources                map[string]likeVectorSource
	queryText              []string
	beginOptions           driver.TxOptions
	prepareCount, commits  int
	commitError            error
	cancelOnPrepare        context.CancelFunc
	oversized, wrongLength bool
	wrongCollation         bool
}

var vectorSQLMemoryDBs sync.Map
var vectorSQLMemorySequence atomic.Uint64

type vectorSQLMemoryDriver struct{}
type vectorSQLMemoryConn struct{ state *vectorSQLMemory }
type vectorSQLMemoryTx struct{ state *vectorSQLMemory }
type vectorSQLMemoryStmt struct {
	state *vectorSQLMemory
	query string
}
type vectorSQLMemoryRows struct {
	columns []string
	rows    [][]driver.Value
}

func init() { sql.Register("cap10-vector-pure", vectorSQLMemoryDriver{}) }
func (vectorSQLMemoryDriver) Open(name string) (driver.Conn, error) {
	value, exists := vectorSQLMemoryDBs.Load(name)
	if !exists {
		return nil, errors.New("unregistered pure test database")
	}
	return &vectorSQLMemoryConn{state: value.(*vectorSQLMemory)}, nil
}
func (conn *vectorSQLMemoryConn) Close() error { return nil }
func (conn *vectorSQLMemoryConn) Begin() (driver.Tx, error) {
	return conn.BeginTx(context.Background(), driver.TxOptions{})
}
func (conn *vectorSQLMemoryConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn.state.mu.Lock()
	conn.state.beginOptions = options
	conn.state.mu.Unlock()
	return &vectorSQLMemoryTx{conn.state}, nil
}
func (conn *vectorSQLMemoryConn) Prepare(query string) (driver.Stmt, error) {
	return conn.PrepareContext(context.Background(), query)
}
func (conn *vectorSQLMemoryConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	conn.state.mu.Lock()
	conn.state.prepareCount++
	cancel := conn.state.cancelOnPrepare
	conn.state.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &vectorSQLMemoryStmt{conn.state, query}, nil
}
func (conn *vectorSQLMemoryConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn.state.mu.Lock()
	conn.state.queryText = append(conn.state.queryText, query)
	conn.state.mu.Unlock()
	switch {
	case strings.Contains(query, "INFORMATION_SCHEMA.COLUMNS"):
		collation := "utf8mb4_0900_ai_ci"
		if conn.state.wrongCollation {
			collation = "utf8mb4_unicode_ci"
		}
		return vectorMemoryRows([][]driver.Value{{"utf8mb4", collation}}, 2), nil
	case query == "SELECT CONNECTION_ID()":
		return vectorMemoryRows([][]driver.Value{{int64(71)}}, 1), nil
	case strings.Contains(query, "FROM tb_community_projection_control"):
		return vectorMemoryRows([][]driver.Value{{"feed-v1", nil}, {"follow-v1", nil}}, 2), nil
	case strings.Contains(query, "FROM tb_note_search_control"):
		return vectorMemoryRows([][]driver.Value{{"search-v1", "notes", "notes-build-one", nil}}, 4), nil
	case strings.Contains(query, "FROM tb_blog_like"):
		var values [][]driver.Value
		for _, fact := range conn.state.facts {
			values = append(values, []driver.Value{fact.NoteID, fact.UserID, fact.Version, int64(fact.Active)})
		}
		return vectorMemoryRows(values, 4), nil
	case strings.Contains(query, "COUNT(*) FROM tb_community_consumer_failure"):
		return vectorMemoryRows([][]driver.Value{{int64(0)}}, 1), nil
	case strings.Contains(query, "MAX(id)"):
		return vectorMemoryRows([][]driver.Value{{int64(101)}}, 1), nil
	default:
		return nil, errors.New("unexpected statement in pure SQL adapter test")
	}
}
func (tx *vectorSQLMemoryTx) Commit() error {
	tx.state.mu.Lock()
	defer tx.state.mu.Unlock()
	tx.state.commits++
	return tx.state.commitError
}
func (tx *vectorSQLMemoryTx) Rollback() error   { return nil }
func (stmt *vectorSQLMemoryStmt) Close() error  { return nil }
func (stmt *vectorSQLMemoryStmt) NumInput() int { return strings.Count(stmt.query, "?") }
func (stmt *vectorSQLMemoryStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("writes forbidden")
}
func (stmt *vectorSQLMemoryStmt) Query(values []driver.Value) (driver.Rows, error) {
	args := make([]driver.NamedValue, len(values))
	for i, value := range values {
		args[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return stmt.QueryContext(context.Background(), args)
}
func (stmt *vectorSQLMemoryStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stmt.state.mu.Lock()
	stmt.state.queryText = append(stmt.state.queryText, stmt.query)
	stmt.state.mu.Unlock()
	var values [][]driver.Value
	for index := 0; index < len(args); index++ {
		id := args[index].Value.(string)
		row, exists := stmt.state.sources[id]
		if !exists {
			continue
		}
		length := int64(len(row.Payload))
		var payload driver.Value = []byte(row.Payload)
		if stmt.state.oversized {
			length = likeVectorPayloadBytes + 1
			payload = nil
		}
		if stmt.state.wrongLength {
			length++
		}
		value := []driver.Value{int64(row.ID), row.EventID, row.EventType, row.AggregateType, row.AggregateID, row.Version, length, payload, row.DeliveryStatus}
		for _, projection := range []string{"follow-v1", "feed-v1", "search-v1"} {
			var receipt *likeVectorReceipt
			for i := range row.Receipts {
				if row.Receipts[i].Projection == projection {
					receipt = &row.Receipts[i]
				}
			}
			if receipt == nil {
				value = append(value, nil, nil, nil)
			} else {
				value = append(value, receipt.EventID, append([]byte(nil), receipt.Hash[:]...), receipt.State)
			}
		}
		values = append(values, value)
	}
	return vectorMemoryRows(values, 18), nil
}
func vectorMemoryRows(values [][]driver.Value, size int) *vectorSQLMemoryRows {
	columns := make([]string, size)
	for index := range columns {
		columns[index] = fmt.Sprint(index)
	}
	return &vectorSQLMemoryRows{columns: columns, rows: values}
}
func (rows *vectorSQLMemoryRows) Columns() []string { return rows.columns }
func (rows *vectorSQLMemoryRows) Close() error      { return nil }
func (rows *vectorSQLMemoryRows) Next(dest []driver.Value) error {
	if len(rows.rows) == 0 {
		return io.EOF
	}
	copy(dest, rows.rows[0])
	rows.rows = rows.rows[1:]
	return nil
}

func vectorMemoryControls() likeVectorControls {
	var result likeVectorControls
	for index, raw := range []string{`{"Projection":"follow-v1","RebuiltAt":{"String":"","Valid":false}}`, `{"Projection":"feed-v1","RebuiltAt":{"String":"","Valid":false}}`, `{"Name":"search-v1","IndexAlias":"notes","ActiveGeneration":"notes-build-one","RebuiltAt":{"String":"","Valid":false}}`} {
		sum := sha256.Sum256([]byte(raw))
		result[index] = hex.EncodeToString(sum[:])
	}
	return result
}
func vectorMemoryDatabase(t *testing.T, versions int64) (*sql.DB, *vectorSQLMemory, *likeVectorEngine) {
	t.Helper()
	state := &vectorSQLMemory{facts: []likeVectorFact{vectorTestFact("101", "1", versions)}, sources: map[string]likeVectorSource{}}
	limits := vectorTestLimits()
	limits.MaxNewEvents = 1024
	engine, err := newLikeVector([]likeVectorPair{{"101", "1"}}, nil, vectorMemoryControls(), limits)
	if err != nil {
		t.Fatal(err)
	}
	for version := int64(1); version <= versions; version++ {
		key := likeVectorKey{EventID: likeVectorEventID(likeVectorPair{"101", "1"}, version), NoteID: "101", UserID: "1", Version: version}
		state.sources[key.EventID] = vectorTestRow(t, key, uint64(version), "DONE", 3)
	}
	name := fmt.Sprint(vectorSQLMemorySequence.Add(1))
	vectorSQLMemoryDBs.Store(name, state)
	db, err := sql.Open("cap10-vector-pure", name)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close(); vectorSQLMemoryDBs.Delete(name) })
	return db, state, engine
}

func TestLikeVectorSQLReadOnlyCommitAndStatementReuse(t *testing.T) {
	db, state, engine := vectorMemoryDatabase(t, 513)
	before := engine.Snapshot()
	candidate, stats, err := readLikeVectorSQL(context.Background(), db, engine, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.SQLCommitted || stats.SourcePrepares != 2 || stats.Queries != 10 || stats.SourceRows != 513 || stats.RequestedKeys != 513 || stats.SourceValueBytes <= 0 || stats.ConnectionID != 71 || stats.SourceCollation != "utf8mb4_0900_ai_ci" {
		t.Fatalf("incorrect SQL accounting: %+v", stats)
	}
	state.mu.Lock()
	options, prepares, commits := state.beginOptions, state.prepareCount, state.commits
	state.mu.Unlock()
	if !options.ReadOnly || options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) || prepares != 2 || commits != 1 {
		t.Fatal("not one real read-only RR commit with reused prepared SELECTs")
	}
	if !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("SQL success published before whole-sample gate")
	}
	view := candidate.SQLView()
	if view.Counts.Events != 513 || view.Counts.Done != 513 || view.Receipts[2].Ignored != 513 {
		t.Fatalf("wrong candidate: %+v", view)
	}
	view.Receipts[0].Ignored = 999
	if candidate.SQLView().Receipts[0].Ignored != 513 {
		t.Fatal("candidate view aliases its stored receipts")
	}
	if err := candidate.Accept(true); err != nil {
		t.Fatal(err)
	}
	if engine.Snapshot().SeenEvents != 513 {
		t.Fatal("successful whole sample did not publish")
	}
}

func TestLikeVectorSQLFailureAndKafkaGateDoNotPublish(t *testing.T) {
	for _, kind := range []string{"commit", "canceled-prepare", "oversized-payload", "wrong-length", "missing-source", "collation", "kafka"} {
		t.Run(kind, func(t *testing.T) {
			db, state, engine := vectorMemoryDatabase(t, 1)
			before := engine.Snapshot()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("read commit acknowledgement lost")
			switch kind {
			case "commit":
				state.commitError = sentinel
			case "canceled-prepare":
				state.cancelOnPrepare = cancel
			case "oversized-payload":
				state.oversized = true
			case "wrong-length":
				state.wrongLength = true
			case "missing-source":
				delete(state.sources, "LIKE:101:1:1")
			case "collation":
				state.wrongCollation = true
			}
			candidate, stats, err := readLikeVectorSQL(ctx, db, engine, 0)
			if kind == "kafka" {
				if err != nil {
					t.Fatal(err)
				}
				if err := candidate.Accept(false); !errors.Is(err, errLikeVectorRejected) {
					t.Fatal("Kafka failure accepted")
				}
				if err := candidate.Accept(true); !errors.Is(err, errLikeVectorStale) {
					t.Fatal("failed Kafka sample relabeled")
				}
			} else {
				if err == nil || candidate != nil || stats.SQLCommitted {
					t.Fatalf("failed SQL returned a publishable candidate: %+v %v", stats, err)
				}
				if kind == "commit" && !errors.Is(err, sentinel) {
					t.Fatal("commit error hidden")
				}
				if kind == "canceled-prepare" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation hidden")
				}
			}
			if !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("failed sample advanced vector state")
			}
		})
	}
}

func TestLikeVectorSQLRawCapBeforeRetainingRows(t *testing.T) {
	db, _, _ := vectorMemoryDatabase(t, 1)
	query, _ := likeVectorSourceQuery(1, "utf8mb4_0900_ai_ci")
	statement, err := db.Prepare(query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	rows, err := statement.Query("LIKE:101:1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stats := &likeVectorSQLStats{SourceValueBytes: likeVectorSQLValueBytes - 1}
	var retained []likeVectorSource
	if err := likeVectorScanSources(rows, stats, &retained); !errors.Is(err, errLikeVectorLimit) || len(retained) != 0 {
		t.Fatal("raw bytes copied before cumulative cap rejection")
	}
}

func TestLikeVectorSQLBoundedQueryContract(t *testing.T) {
	factQuery, factArgs, err := likeVectorFactsQuery([]likeVectorPair{{"101", "1"}, {"102", "2"}})
	if err != nil || strings.Count(factQuery, "?") != 3 || !strings.Contains(factQuery, "WHERE blog_id IN (?,?)") || !strings.Contains(factQuery, "LIMIT ?") || len(factArgs) != 3 || factArgs[2] != 3 {
		t.Fatal("full selected-note fact rows/extra-row cap query changed")
	}
	if _, _, err := likeVectorFactsQuery(nil); !errors.Is(err, errLikeVectorInput) {
		t.Fatal("unbounded empty fact query accepted")
	}
	for _, size := range []int{1, 2, 256} {
		query, err := likeVectorSourceQuery(size, "utf8mb4_0900_ai_ci")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(query, "?") != size || strings.Count(query, "FORCE INDEX (uk_community_event)") != 1 || strings.Count(query, "FORCE INDEX (PRIMARY)") != 3 || strings.Count(query, "CONVERT(o.event_id USING ascii) COLLATE ascii_bin") != 3 || strings.Count(query, "UNION ALL") != size-1 || !strings.Contains(query, "CASE WHEN OCTET_LENGTH(o.payload)<=1048576 THEN o.payload ELSE NULL END") || !strings.Contains(query, " LIMIT 256) k") || !strings.Contains(query, "STRAIGHT_JOIN tb_community_outbox o") || !strings.Contains(query, "o.event_id=k.event_id AND BINARY o.event_id=BINARY k.event_id") || strings.Count(query, "CAST(? AS CHAR CHARACTER SET utf8mb4) COLLATE utf8mb4_0900_ai_ci") != size {
			t.Fatal("point-query/binary/payload contract changed")
		}
		for alias, projection := range map[string]string{"rf": "follow-v1", "rd": "feed-v1", "rs": "search-v1"} {
			if strings.Count(query, "BINARY "+alias+".projection=BINARY '"+projection+"'") != 1 {
				t.Fatal("projection PAD SPACE alias guard missing")
			}
		}
	}
	for _, size := range []int{0, 257, -1} {
		if _, err := likeVectorSourceQuery(size, "utf8mb4_0900_ai_ci"); !errors.Is(err, errLikeVectorInput) {
			t.Fatal("unbounded SQL branches accepted")
		}
	}
	for _, collation := range []string{"", "ascii_bin", "utf8mb4_unicode_ci", "utf8mb4_0900_ai_ci;DROP TABLE x"} {
		if _, err := likeVectorSourceQuery(1, collation); !errors.Is(err, errLikeVectorInput) {
			t.Fatal("unreviewed/injectable collation accepted")
		}
	}
	if candidate, _, err := readLikeVectorSQL(context.Background(), nil, nil, 0); candidate != nil || !errors.Is(err, errLikeVectorInput) {
		t.Fatal("missing SQL dependencies accepted")
	}
}
