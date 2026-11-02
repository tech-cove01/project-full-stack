package communityrelay

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type pointScript struct {
	base                   batchScript
	calls                  [][]uint64
	park                   []bool
	failCall, mismatchCall int
	cause                  error
}
type pointConnector struct{ script *pointScript }
type pointConnection struct {
	*batchConnection
	script *pointScript
}

func (connector pointConnector) Driver() driver.Driver { return batchDriver{} }
func (connector pointConnector) Connect(context.Context) (driver.Conn, error) {
	return &pointConnection{batchConnection: &batchConnection{script: &connector.script.base}, script: connector.script}, nil
}
func (connection *pointConnection) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	park := strings.Contains(query, "NEEDS_REVIEW")
	var ids []uint64
	if strings.Contains(query, " AS selected ") {
		count := len(args) / 2
		for i := 0; i < count; i++ {
			ids = append(ids, uint64(args[2*i].Value.(int64)))
		}
	} else {
		position := 0
		if !park {
			position = 1
		}
		ids = []uint64{uint64(args[position].Value.(int64))}
	}
	connection.script.calls = append(connection.script.calls, ids)
	connection.script.park = append(connection.script.park, park)
	if len(connection.script.calls) == connection.script.failCall {
		return nil, connection.script.cause
	}
	rows := int64(len(ids))
	if len(connection.script.calls) == connection.script.mismatchCall {
		rows--
	}
	return driver.RowsAffected(rows), nil
}
func pointRepository(t *testing.T, script *pointScript) *Repository {
	t.Helper()
	db := sql.OpenDB(pointConnector{script})
	t.Cleanup(func() { _ = db.Close() })
	orm, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return NewRepository(orm)
}
func TestClaimPointSQLBoundsTypesAndOriginalSingleton(t *testing.T) {
	for _, n := range []int{2, 32, 64} {
		for _, park := range []bool{false, true} {
			events := make([]Event, n)
			for i := range events {
				events[i] = Event{ID: uint64(i + 1), Attempts: i % 9}
			}
			query, args, err := claimPointSQL(events, park)
			want := 2 * n
			if !park {
				want++
			}
			if err != nil || len(args) != want || strings.Count(query, "?") != want || strings.Count(query, "UNION ALL") != n-1 ||
				!strings.Contains(query, "STRAIGHT_JOIN tb_community_outbox AS o FORCE INDEX (PRIMARY) ON o.id=selected.id") ||
				!strings.HasSuffix(query, "WHERE o.attempts=selected.attempts") {
				t.Fatal("bounded driver/fence changed")
			}
			for i, event := range events {
				if args[2*i] != event.ID || args[2*i+1] != event.Attempts {
					t.Fatal("typed per-row fence reordered")
				}
			}
			if park && strings.Contains(query, "attempts=") && strings.Contains(query, "o.attempts=o.attempts+1") {
				t.Fatal("parking spent an attempt")
			}
		}
	}
	max := ^uint64(0)
	_, args, err := claimPointSQL([]Event{{ID: max, Attempts: 0}, {ID: max - 1, Attempts: 7}}, false)
	if err != nil || args[0] != max || args[2] != max-1 {
		t.Fatal("uint64 boundary passed through a signed or float conversion")
	}
	for _, park := range []bool{false, true} {
		query, args, err := claimPointSQL([]Event{{ID: 42, Attempts: 8}}, park)
		want := pointOriginalLease
		wantArgs := []any{leaseSeconds, uint64(42), 8}
		if park {
			want = pointOriginalPark
			wantArgs = []any{uint64(42), 8}
		}
		if err != nil || query != want || !reflect.DeepEqual(args, wantArgs) {
			t.Fatal("singleton original SQL/arguments changed")
		}
	}
	for _, events := range [][]Event{nil, {{ID: 0}}, {{ID: 1}, {ID: 1}}, make([]Event, 65), {{ID: 1, Attempts: -1}}} {
		if _, _, err := claimPointSQL(events, false); !errors.Is(err, ErrInvalidBatch) {
			t.Fatal("invalid bounded driver accepted")
		}
	}
}
func TestClaimPointKeepsByteCutoffParkingAndOversizedSingleton(t *testing.T) {
	a, b, c, d, e := boundedEvent(1, "a"), boundedEvent(2, "b"), boundedEvent(3, "c"), boundedEvent(4, "d"), boundedEvent(5, "e")
	a.Attempts, c.Attempts, e.Attempts = 8, 9, 8
	script := &pointScript{base: batchScript{rows: []Event{a, b, c, d, e}}}
	got, err := pointRepository(t, script).claimBatch(context.Background(), 5, int(eventEncodedUpperBound(b)))
	if err != nil || len(got) != 1 || got[0].ID != 2 || got[0].Attempts != 1 ||
		!reflect.DeepEqual(script.calls, [][]uint64{{1, 3}, {2}}) || !reflect.DeepEqual(script.park, []bool{true, false}) ||
		script.base.commits != 1 || script.base.rollbacks != 0 || script.base.beginOptions[0].Isolation != driver.IsolationLevel(sql.LevelReadCommitted) {
		t.Fatal("original ordered cutoff/exhausted/RC decisions changed")
	}
	large := boundedEvent(2, strings.Repeat("x", 10000))
	script = &pointScript{base: batchScript{rows: []Event{a, large, c, d}}}
	got, err = pointRepository(t, script).claimBatch(context.Background(), 4, 1)
	if err != nil || len(got) != 1 || got[0].ID != 2 || !reflect.DeepEqual(script.calls, [][]uint64{{1}, {2}}) {
		t.Fatal("first oversized singleton touched its suffix")
	}
}
func TestClaimPointRollsBackBothListsAndKeepsAmbiguousCommitError(t *testing.T) {
	cause := errors.New("injected second SQL failure")
	for _, mismatch := range []bool{false, true} {
		script := &pointScript{base: batchScript{rows: []Event{{ID: 1, Attempts: 8}, {ID: 2, Attempts: 8}, {ID: 3}, {ID: 4}}}, cause: cause}
		want := cause
		if mismatch {
			script.mismatchCall = 2
			want = ErrLeaseLost
		} else {
			script.failCall = 2
		}
		got, err := pointRepository(t, script).claimBatch(context.Background(), 4, 0)
		if got != nil || !errors.Is(err, want) || script.base.commits != 0 || script.base.rollbacks != 1 || !reflect.DeepEqual(script.calls, [][]uint64{{1, 2}, {3, 4}}) {
			t.Fatal("second-group failure exposed prefix or escaped whole transaction rollback")
		}
	}
	script := &pointScript{base: batchScript{rows: []Event{{ID: 1}, {ID: 2}}, commitError: cause}}
	if got, err := pointRepository(t, script).claimBatch(context.Background(), 2, 0); got != nil || !errors.Is(err, cause) || script.base.commits != 1 {
		t.Fatal("commit uncertainty was replaced with publishable output or fabricated rollback proof")
	}
}
