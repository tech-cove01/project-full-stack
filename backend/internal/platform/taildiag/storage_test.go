package taildiag

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testDriver struct{}
type testConnection struct{}
type testTransaction struct{}
type testRows struct{ sent bool }

func (testDriver) Open(string) (driver.Conn, error)        { return testConnection{}, nil }
func (testConnection) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (testConnection) Close() error                        { return nil }
func (testConnection) Begin() (driver.Tx, error)           { return testTransaction{}, nil }
func (testConnection) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	return testTransaction{}, ctx.Err()
}
func (testConnection) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return &testRows{}, ctx.Err()
}
func (testConnection) ExecContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), ctx.Err()
}
func (testTransaction) Commit() error   { return nil }
func (testTransaction) Rollback() error { return nil }
func (*testRows) Columns() []string     { return []string{"n"} }
func (*testRows) Close() error          { return nil }
func (rows *testRows) Next(values []driver.Value) error {
	if rows.sent {
		return io.EOF
	}
	rows.sent = true
	values[0] = int64(1)
	return nil
}

func init() { sql.Register("taildiag-no-network", testDriver{}) }

func TestSQLWrapperPreservesTransactionAndPoolInterfaces(t *testing.T) {
	pool, err := sql.Open("taildiag-no-network", "")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	observed := &observedPool{DB: pool}
	var _ gorm.ConnPool = observed
	var _ gorm.ConnPoolBeginner = observed
	var _ gorm.GetDBConnector = observed
	if got, _ := observed.GetDBConn(); got != pool {
		t.Fatal("pool identity changed")
	}
	recorder := New()
	ctx, finish := Start(context.Background(), recorder, false)
	defer finish(false)
	connection, err := observed.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connection.(*observedTransaction); !ok {
		t.Fatal("enabled transaction missing lifetime observation")
	}
	var _ gorm.Tx = connection.(*observedTransaction)
	rows, err := connection.QueryContext(ctx, "private-query", "private-argument")
	if err != nil || !rows.Next() {
		t.Fatal("query result changed")
	}
	var got int
	if err = rows.Scan(&got); err != nil || got != 1 {
		t.Fatal("row scan changed")
	}
	_ = rows.Close()
	if err = connection.(gorm.TxCommitter).Commit(); err != nil {
		t.Fatal(err)
	}
	_ = connection.(gorm.TxCommitter).Rollback()
	out := recorder.Snapshot()
	if out.Stages[SQLBegin].Count != 1 || out.Stages[SQLCall].Count != 1 || out.Stages[SQLHold].Count != 1 || out.Stages[SQLHold].Active != 0 {
		t.Fatal("transaction boundaries must be measured once")
	}
	plain, err := observed.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plain.(*sql.Tx); !ok {
		t.Fatal("untraced transaction should retain original concrete type")
	}
	_ = plain.(gorm.TxCommitter).Rollback()
}

func TestStorageHooksNeverReadTextOrArgumentsAndPropagateCancellation(t *testing.T) {
	recorder := New()
	ctx, cancel := context.WithCancel(context.Background())
	ctx, finish := Start(ctx, recorder, true)
	defer finish(true)
	var called atomic.Bool
	log := observedLogger{Interface: logger.Default.LogMode(logger.Silent)}
	log.Trace(ctx, time.Now(), func() (string, int64) { called.Store(true); return "private SQL", 1 }, nil)
	if called.Load() {
		t.Fatal("diagnostics must not request SQL text")
	}
	command := redis.NewCmd(ctx, "get", "private-token-key")
	cancel()
	err := (redisObserver{}).ProcessHook(func(nextCtx context.Context, cmd redis.Cmder) error {
		if cmd != command {
			t.Fatal("command identity changed")
		}
		return nextCtx.Err()
	})(ctx, command)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("hook changed cancellation error")
	}
	out := recorder.Snapshot()
	if out.Stages[SQLStatement].Count != 1 || out.Stages[RedisCommand].Count != 1 || out.Stages[RedisCommand].Errors != 1 || out.Stages[RedisCommand].Active != 0 {
		t.Fatal("hook totals or cancellation observation changed")
	}
}
