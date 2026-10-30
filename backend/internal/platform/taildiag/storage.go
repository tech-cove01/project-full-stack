package taildiag

import (
	"context"
	"database/sql"
	"net"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Instrument installs observations on the existing ORM and Redis client before
// requests start. It does not replace the driver, alter pools, add queries or
// inspect statement/command text. Only contexts started by Start are recorded.
func Instrument(db *gorm.DB, sqlPool *sql.DB, redisPool *redis.Client) {
	pool := &observedPool{DB: sqlPool}
	db.Config.ConnPool = pool
	db.Statement.ConnPool = pool
	db.Config.Logger = observedLogger{Interface: db.Config.Logger}
	redisPool.AddHook(redisObserver{})
}

type observedPool struct{ *sql.DB }

func (pool *observedPool) GetDBConn() (*sql.DB, error) { return pool.DB, nil }

func (pool *observedPool) BeginTx(ctx context.Context, options *sql.TxOptions) (gorm.ConnPool, error) {
	span := Begin(ctx, SQLBegin, 0)
	tx, err := pool.DB.BeginTx(ctx, options)
	span.End(err != nil)
	if err != nil || fromContext(ctx) == nil {
		return tx, err
	}
	return &observedTransaction{Tx: tx, hold: Begin(ctx, SQLHold, 0)}, nil
}

func (pool *observedPool) QueryContext(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	span := Begin(ctx, SQLCall, 0)
	rows, err := pool.DB.QueryContext(ctx, statement, args...)
	span.End(err != nil)
	return rows, err
}

func (pool *observedPool) ExecContext(ctx context.Context, statement string, args ...any) (sql.Result, error) {
	span := Begin(ctx, SQLExec, 0)
	result, err := pool.DB.ExecContext(ctx, statement, args...)
	span.End(err != nil)
	return result, err
}

func (pool *observedPool) PrepareContext(ctx context.Context, statement string) (*sql.Stmt, error) {
	span := Begin(ctx, SQLPrepare, 0)
	result, err := pool.DB.PrepareContext(ctx, statement)
	span.End(err != nil)
	return result, err
}

type observedTransaction struct {
	*sql.Tx
	hold *Span
	once sync.Once
}

func (tx *observedTransaction) Commit() error {
	err := tx.Tx.Commit()
	tx.once.Do(func() { tx.hold.End(err != nil) })
	return err
}

func (tx *observedTransaction) Rollback() error {
	err := tx.Tx.Rollback()
	tx.once.Do(func() { tx.hold.End(err != nil) })
	return err
}

func (tx *observedTransaction) QueryContext(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	span := Begin(ctx, SQLCall, 0)
	rows, err := tx.Tx.QueryContext(ctx, statement, args...)
	span.End(err != nil)
	return rows, err
}

func (tx *observedTransaction) ExecContext(ctx context.Context, statement string, args ...any) (sql.Result, error) {
	span := Begin(ctx, SQLExec, 0)
	result, err := tx.Tx.ExecContext(ctx, statement, args...)
	span.End(err != nil)
	return result, err
}

func (tx *observedTransaction) PrepareContext(ctx context.Context, statement string) (*sql.Stmt, error) {
	span := Begin(ctx, SQLPrepare, 0)
	result, err := tx.Tx.PrepareContext(ctx, statement)
	span.End(err != nil)
	return result, err
}

type observedLogger struct{ logger.Interface }

func (log observedLogger) LogMode(level logger.LogLevel) logger.Interface {
	return observedLogger{Interface: log.Interface.LogMode(level)}
}

func (log observedLogger) Trace(ctx context.Context, started time.Time, text func() (string, int64), err error) {
	// In particular do not call text(): diagnostics must not materialize SQL,
	// parameters or row/identity labels. The application's silent logger stays so.
	Record(ctx, SQLStatement, started, err != nil, 0)
	log.Interface.Trace(ctx, started, text, err)
}

type redisObserver struct{}

func (redisObserver) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		span := Begin(ctx, RedisDial, 0)
		connection, err := next(ctx, network, address)
		span.End(err != nil)
		return connection, err
	}
}

func (redisObserver) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, command redis.Cmder) error {
		span := Begin(ctx, RedisCommand, 0)
		err := next(ctx, command)
		span.End(err != nil)
		return err
	}
}

func (redisObserver) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error {
		span := Begin(ctx, RedisPipeline, len(commands))
		err := next(ctx, commands)
		span.End(err != nil)
		return err
	}
}
