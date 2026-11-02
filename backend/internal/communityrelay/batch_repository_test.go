package communityrelay

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This driver tests transaction/error composition only. Eligibility, row locks
// and durable rollback are covered by the isolated real-MySQL regressions.
type batchScript struct {
	rows                   []Event
	execIDs                []uint64
	beginOptions           []driver.TxOptions
	limit                  int64
	zeroAt, failAt         int
	execError, commitError error
	commits, rollbacks     int
}
type batchConnector struct{ script *batchScript }
type batchDriver struct{}
type batchConnection struct{ script *batchScript }
type batchTransaction struct{ script *batchScript }
type batchRows struct {
	rows  []Event
	index int
}

func (connector batchConnector) Connect(context.Context) (driver.Conn, error) {
	return &batchConnection{script: connector.script}, nil
}
func (batchConnector) Driver() driver.Driver         { return batchDriver{} }
func (batchDriver) Open(string) (driver.Conn, error) { return nil, errors.New("connector required") }
func (*batchConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*batchConnection) Close() error { return nil }
func (connection *batchConnection) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}
func (connection *batchConnection) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	connection.script.beginOptions = append(connection.script.beginOptions, options)
	return &batchTransaction{script: connection.script}, nil
}
func (connection *batchConnection) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(query, "FOR UPDATE SKIP LOCKED") || !strings.Contains(query, "idx_community_active_claim") || len(args) != 1 {
		return nil, errors.New("unexpected claim query")
	}
	connection.script.limit = args[0].Value.(int64)
	return &batchRows{rows: connection.script.rows}, nil
}
func (connection *batchConnection) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	idIndex := 0
	if strings.Contains(query, "attempts=attempts+1") {
		idIndex = 1
	}
	id := args[idIndex].Value.(int64)
	connection.script.execIDs = append(connection.script.execIDs, uint64(id))
	position := len(connection.script.execIDs)
	if position == connection.script.failAt {
		return nil, connection.script.execError
	}
	if position == connection.script.zeroAt {
		return driver.RowsAffected(0), nil
	}
	return driver.RowsAffected(1), nil
}
func (transaction *batchTransaction) Commit() error {
	transaction.script.commits++
	return transaction.script.commitError
}
func (transaction *batchTransaction) Rollback() error { transaction.script.rollbacks++; return nil }
func (*batchRows) Columns() []string {
	return []string{"id", "event_id", "aggregate_type", "aggregate_id", "aggregate_version", "event_type", "payload", "attempts"}
}
func (*batchRows) Close() error { return nil }
func (rows *batchRows) Next(values []driver.Value) error {
	if rows.index == len(rows.rows) {
		return io.EOF
	}
	event := rows.rows[rows.index]
	rows.index++
	copy(values, []driver.Value{int64(event.ID), event.EventID, event.AggregateType, event.AggregateID,
		int64(event.AggregateVersion), event.EventType, event.Payload, int64(event.Attempts)})
	return nil
}

func scriptedBatchRepository(t *testing.T, script *batchScript) *Repository {
	t.Helper()
	db := sql.OpenDB(batchConnector{script: script})
	t.Cleanup(func() { _ = db.Close() })
	orm, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: db, SkipInitializeWithVersion: true}),
		&gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return NewRepository(orm)
}

func TestBatchInputIsRejectedBeforeDependencyAccess(t *testing.T) {
	repository := NewRepository(nil)
	for _, limit := range []int{-1, 0, MaxBatchSize + 1} {
		if events, err := repository.ClaimBatch(context.Background(), limit); !errors.Is(err, ErrInvalidBatch) || events != nil {
			t.Fatal("invalid claim bounds must fail before database access")
		}
	}
	for _, events := range [][]Event{nil, {}, {{ID: 0, Attempts: 1}}, {{ID: 1}}, {{ID: 1, Attempts: 9}},
		{{ID: 1, Attempts: 1}, {ID: 1, Attempts: 2}}, make([]Event, MaxBatchSize+1)} {
		if err := repository.DoneBatch(context.Background(), events); !errors.Is(err, ErrInvalidBatch) {
			t.Fatal("invalid IDs, fences or bounds must fail before database access")
		}
	}
}

func TestClaimBatchCommitsOnceAndPreservesPerRowAttemptFences(t *testing.T) {
	script := &batchScript{rows: []Event{{ID: 1, Attempts: 0}, {ID: 2, Attempts: 8}, {ID: 3, Attempts: 7}}}
	events, err := scriptedBatchRepository(t, script).ClaimBatch(context.Background(), MaxBatchSize)
	if err != nil || len(events) != 2 || events[0].ID != 1 || events[0].Attempts != 1 || events[1].ID != 3 || events[1].Attempts != 8 {
		t.Fatal("only committed, non-exhausted leases with incremented individual fences may be returned")
	}
	if script.limit != MaxBatchSize || script.commits != 1 || script.rollbacks != 0 ||
		len(script.beginOptions) != 1 || script.beginOptions[0].Isolation != driver.IsolationLevel(sql.LevelReadCommitted) ||
		!reflect.DeepEqual(script.execIDs, []uint64{1, 2, 3}) {
		t.Fatal("one bounded RC transaction must claim/park rows in their original ID order")
	}
}

func TestClaimBatchReturnsNoPublishableSliceOnStatementOrCommitError(t *testing.T) {
	cause := errors.New("synthetic database failure")
	for _, commit := range []bool{false, true} {
		script := &batchScript{rows: []Event{{ID: 1}, {ID: 2}}}
		if commit {
			script.commitError = cause
		} else {
			script.failAt, script.execError = 2, cause
		}
		events, err := scriptedBatchRepository(t, script).ClaimBatch(context.Background(), 2)
		if !errors.Is(err, cause) || events != nil {
			t.Fatal("an unsuccessful batch must expose neither an error replacement nor a leased prefix")
		}
		if !commit && (script.rollbacks != 1 || script.commits != 0) {
			t.Fatal("statement failure must abort the claim transaction")
		}
		if commit && script.commits != 1 {
			t.Fatal("the original ambiguous commit error must remain distinguishable from statement rollback")
		}
	}
}

func TestDoneBatchSortsACopyAndCommitsOnce(t *testing.T) {
	events := make([]Event, MaxBatchSize)
	for i := range events {
		events[i] = Event{ID: uint64(MaxBatchSize - i), Attempts: i%8 + 1}
	}
	before := append([]Event(nil), events...)
	script := &batchScript{}
	if err := scriptedBatchRepository(t, script).DoneBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, before) || script.commits != 1 || script.rollbacks != 0 || len(script.execIDs) != MaxBatchSize {
		t.Fatal("successful done batch must commit once without reordering caller data")
	}
	for i, id := range script.execIDs {
		if id != uint64(i+1) {
			t.Fatal("completion locks must be taken in ascending ID order")
		}
	}
}

func TestDoneBatchLostFenceOrDatabaseErrorAbortsWholeTransaction(t *testing.T) {
	cause := errors.New("synthetic update failure")
	for _, lost := range []bool{true, false} {
		script := &batchScript{}
		want := cause
		if lost {
			script.zeroAt, want = 2, ErrLeaseLost
		} else {
			script.failAt, script.execError = 2, cause
		}
		err := scriptedBatchRepository(t, script).DoneBatch(context.Background(), []Event{{ID: 3, Attempts: 1}, {ID: 1, Attempts: 1}, {ID: 2, Attempts: 2}})
		if !errors.Is(err, want) || script.commits != 0 || script.rollbacks != 1 || !reflect.DeepEqual(script.execIDs, []uint64{1, 2}) {
			t.Fatal("stale/failed middle update must abort, not commit an earlier prefix or touch later IDs")
		}
	}
}

func TestDoneBatchReturnsCommitErrorWithoutClaimingConfirmedRollback(t *testing.T) {
	cause := errors.New("synthetic ambiguous commit")
	script := &batchScript{commitError: cause}
	err := scriptedBatchRepository(t, script).DoneBatch(context.Background(), []Event{{ID: 2, Attempts: 1}, {ID: 1, Attempts: 1}})
	if !errors.Is(err, cause) || script.commits != 1 || !reflect.DeepEqual(script.execIDs, []uint64{1, 2}) {
		t.Fatal("commit uncertainty must remain the original error, not a fabricated successful completion")
	}
}
