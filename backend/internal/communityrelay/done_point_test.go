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

type donePointScript struct {
	base      batchScript
	leases    []donePointLease
	calls     []string
	args      [][]driver.NamedValue
	affected  int64
	guard     driver.Value
	failPhase string
	cause     error
}
type donePointConnector struct{ state *donePointScript }
type donePointConn struct {
	*batchConnection
	state *donePointScript
}
type donePointRows struct {
	columns []string
	rows    [][]driver.Value
}

func (connector donePointConnector) Driver() driver.Driver { return batchDriver{} }
func (connector donePointConnector) Connect(context.Context) (driver.Conn, error) {
	return &donePointConn{&batchConnection{script: &connector.state.base}, connector.state}, nil
}
func (connection *donePointConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	phase := "lock"
	if query == donePointFreshGuard {
		phase = "guard"
	}
	connection.state.calls = append(connection.state.calls, phase)
	connection.state.args = append(connection.state.args, append([]driver.NamedValue(nil), args...))
	if connection.state.failPhase == phase {
		return nil, connection.state.cause
	}
	if phase == "guard" {
		return &donePointRows{[]string{"valid"}, [][]driver.Value{{connection.state.guard}}}, nil
	}
	var rows [][]driver.Value
	for _, row := range connection.state.leases {
		var value driver.Value
		if row.OriginalLease.Valid {
			value = row.OriginalLease.String
		}
		rows = append(rows, []driver.Value{int64(row.ID), value})
	}
	return &donePointRows{[]string{"id", "original_lease"}, rows}, nil
}
func (connection *donePointConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection.state.calls = append(connection.state.calls, "update")
	connection.state.args = append(connection.state.args, append([]driver.NamedValue(nil), args...))
	if connection.state.failPhase == "update" {
		return nil, connection.state.cause
	}
	return driver.RowsAffected(connection.state.affected), nil
}
func (rows *donePointRows) Columns() []string { return rows.columns }
func (rows *donePointRows) Close() error      { return nil }
func (rows *donePointRows) Next(values []driver.Value) error {
	if len(rows.rows) == 0 {
		return io.EOF
	}
	copy(values, rows.rows[0])
	rows.rows = rows.rows[1:]
	return nil
}
func donePointRepository(t *testing.T, state *donePointScript) *Repository {
	t.Helper()
	db := sql.OpenDB(donePointConnector{state})
	t.Cleanup(func() { _ = db.Close() })
	orm, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return NewRepository(orm)
}
func donePointLeases() []donePointLease {
	return []donePointLease{{ID: 1, OriginalLease: sql.NullString{String: "2030-01-01 00:00:01.000000", Valid: true}}, {ID: 2, OriginalLease: sql.NullString{String: "2030-01-01 00:00:00.500000", Valid: true}}}
}

func donePointLeasesN(count int) []donePointLease {
	rows := make([]donePointLease, count)
	for index := range rows {
		rows[index] = donePointLease{ID: uint64(index + 1), OriginalLease: sql.NullString{String: "2030-01-01 00:00:01.000000", Valid: true}}
	}
	return rows
}

func TestDoneBatchRoutesSmallAndPointThreshold(t *testing.T) {
	for size := 1; size < donePointMinBatchSize; size++ {
		events := make([]Event, size)
		for index := range events {
			events[index] = Event{ID: uint64(size - index), Attempts: 1}
		}
		old := &batchScript{}
		if err := scriptedBatchRepository(t, old).DoneBatch(context.Background(), events); err != nil || old.execCalls != size || old.commits != 1 || len(old.beginOptions) != 1 || old.beginOptions[0].Isolation != 0 {
			t.Fatalf("N%d must keep the original per-row transaction", size)
		}
	}
	for _, size := range []int{8, 32, MaxBatchSize} {
		events := make([]Event, size)
		for index := range events {
			events[index] = Event{ID: uint64(size - index), Attempts: 1}
		}
		state := &donePointScript{leases: donePointLeasesN(size), affected: int64(size), guard: int64(1)}
		if err := donePointRepository(t, state).DoneBatch(context.Background(), events); err != nil || !reflect.DeepEqual(state.calls, []string{"lock", "update", "guard"}) || state.base.commits != 1 || state.base.rollbacks != 0 || len(state.base.beginOptions) != 1 || state.base.beginOptions[0].Isolation != 0 {
			t.Fatalf("N%d must use the fenced point route", size)
		}
	}
}

func TestDonePointSQLBoundsAndTypedFences(t *testing.T) {
	for _, size := range []int{2, 32, 64} {
		events := make([]Event, size)
		for index := range events {
			events[index] = Event{ID: uint64(size - index), Attempts: 1 + index%8}
		}
		before := append([]Event(nil), events...)
		lock, ids, err := donePointLockSQL(events)
		if err != nil || len(ids) != size || strings.Count(lock, "?") != size || strings.Count(lock, "UNION ALL") != size-1 || !strings.Contains(lock, "PRIMARY) ON o.id=selected.id") || !strings.HasSuffix(lock, "ORDER BY o.id FOR UPDATE") {
			t.Fatal("bounded PK locking shape differs")
		}
		update, args, err := donePointUpdateSQL(events)
		if err != nil || len(args) != 2*size || strings.Count(update, "?") != 2*size || !strings.HasSuffix(update, "o.delivery_status='PROCESSING' AND o.attempts=selected.attempts AND o.lease_until>NOW(3)") {
			t.Fatal("original lease fence lost")
		}
		if ids[0] != uint64(1) || args[0] != uint64(1) || !reflect.DeepEqual(events, before) {
			t.Fatal("input was mutated or ID ordering/type lost")
		}
	}
	max := ^uint64(0)
	_, ids, err := donePointLockSQL([]Event{{ID: max, Attempts: 1}, {ID: max - 1, Attempts: 8}})
	if err != nil || ids[0] != max-1 || ids[1] != max {
		t.Fatal("uint64 boundary lost")
	}
	for _, events := range [][]Event{nil, {{ID: 0, Attempts: 1}}, {{ID: 1, Attempts: 1}, {ID: 1, Attempts: 1}}, {{ID: 1, Attempts: 0}, {ID: 2, Attempts: 1}}, make([]Event, 65)} {
		if _, _, err := donePointUpdateSQL(events); !errors.Is(err, ErrInvalidBatch) {
			t.Fatal("invalid bounded input accepted")
		}
	}
}

func TestDonePointLockUpdateAndFreshGuard(t *testing.T) {
	state := &donePointScript{leases: donePointLeases(), affected: 2, guard: int64(1)}
	events := []Event{{ID: 2, Attempts: 4}, {ID: 1, Attempts: 3}}
	if err := donePointRepository(t, state).donePointBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.calls, []string{"lock", "update", "guard"}) || state.base.commits != 1 || state.base.rollbacks != 0 || len(state.base.beginOptions) != 1 || state.base.beginOptions[0].Isolation != 0 {
		t.Fatal("three-statement default-isolation one-commit contract differs")
	}
	if state.args[2][0].Value != "2030-01-01 00:00:00.500000" {
		t.Fatal("guard did not bind earliest original server lease")
	}
}

func TestDonePointAllKnownLeaseFailuresRollback(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "null-lease", "short-update", "expired-after-update", "null-guard"} {
		t.Run(kind, func(t *testing.T) {
			state := &donePointScript{leases: donePointLeases(), affected: 2, guard: int64(1)}
			switch kind {
			case "missing":
				state.leases = state.leases[:1]
			case "duplicate":
				state.leases[1] = state.leases[0]
			case "null-lease":
				state.leases[1].OriginalLease.Valid = false
			case "short-update":
				state.affected = 1
			case "expired-after-update":
				state.guard = int64(0)
			case "null-guard":
				state.guard = nil
			}
			if err := donePointRepository(t, state).donePointBatch(context.Background(), []Event{{ID: 1, Attempts: 1}, {ID: 2, Attempts: 1}}); !errors.Is(err, ErrLeaseLost) || state.base.commits != 0 || state.base.rollbacks != 1 {
				t.Fatal("known lease loss escaped whole rollback")
			}
			if kind == "expired-after-update" && !reflect.DeepEqual(state.calls, []string{"lock", "update", "guard"}) {
				t.Fatal("fresh post-update guard did not run")
			}
		})
	}
}

func TestDonePointDBAndCommitUncertainty(t *testing.T) {
	cause := errors.New("synthetic DB or lost commit response")
	for _, phase := range []string{"lock", "update", "guard", "commit"} {
		t.Run(phase, func(t *testing.T) {
			state := &donePointScript{leases: donePointLeases(), affected: 2, guard: int64(1), failPhase: phase, cause: cause}
			if phase == "commit" {
				state.base.commitError = cause
			}
			err := donePointRepository(t, state).donePointBatch(context.Background(), []Event{{ID: 1, Attempts: 1}, {ID: 2, Attempts: 1}})
			if !errors.Is(err, cause) || errors.Is(err, ErrLeaseLost) {
				t.Fatal("DB/commit error replaced with known stale fence")
			}
			if phase == "commit" && state.base.commits != 1 {
				t.Fatal("ambiguous commit was not actually attempted")
			}
		})
	}
}

func TestDonePointSingletonAndInvalidInput(t *testing.T) {
	original := &batchScript{rows: nil}
	repo := scriptedBatchRepository(t, original)
	if err := repo.donePointBatch(context.Background(), []Event{{ID: 9, Attempts: 2}}); err != nil || original.commits != 1 || original.execCalls != 1 || !reflect.DeepEqual(original.execIDs, []uint64{9}) {
		t.Fatal("singleton no longer uses original DoneBatch transaction")
	}
	var absent Repository
	for _, events := range [][]Event{nil, {{ID: 0, Attempts: 1}}, {{ID: 1, Attempts: 1}, {ID: 1, Attempts: 1}}} {
		if err := absent.donePointBatch(context.Background(), events); !errors.Is(err, ErrInvalidBatch) {
			t.Fatal("invalid input accessed dependency")
		}
	}
}
