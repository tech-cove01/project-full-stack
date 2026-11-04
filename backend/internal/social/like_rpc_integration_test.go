package social

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type likeRPCContextKey struct{}

var errLikeRPCCommitReceipt = errors.New("injected failure after acknowledged commit")

// These hooks always call the real transaction. Only this test's context can
// inject a stale predicate, cancellation after UPDATE, or post-Commit error.
type likeRPCHooks struct {
	cancel       context.CancelFunc
	updateRows   atomic.Int64
	commitKnown  atomic.Bool
	postReadSeen atomic.Int64
}
type likeRPCPool struct {
	*sql.DB
	hooks *likeRPCHooks
}

func (p *likeRPCPool) GetDBConn() (*sql.DB, error) { return p.DB, nil }
func (p *likeRPCPool) BeginTx(ctx context.Context, options *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &likeRPCTx{Tx: tx, ctx: ctx, hooks: p.hooks}, nil
}
func (p *likeRPCPool) QueryContext(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	if ctx.Value(likeRPCContextKey{}) == "commit-receipt" && strings.HasPrefix(statement, "SELECT b.liked, COALESCE") {
		p.hooks.postReadSeen.Add(1)
	}
	return p.DB.QueryContext(ctx, statement, args...)
}

type likeRPCTx struct {
	*sql.Tx
	ctx   context.Context
	hooks *likeRPCHooks
}

func (tx *likeRPCTx) ExecContext(ctx context.Context, statement string, args ...any) (sql.Result, error) {
	mutation := strings.HasPrefix(statement, "UPDATE tb_blog AS b FORCE INDEX(PRIMARY)")
	if mutation && ctx.Value(likeRPCContextKey{}) == "stale" {
		args = append([]any(nil), args...)
		args[len(args)-1] = args[len(args)-1].(int) + 1
	}
	result, err := tx.Tx.ExecContext(ctx, statement, args...)
	if mutation && err == nil {
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return nil, rowsErr
		}
		tx.hooks.updateRows.Store(rows)
		if ctx.Value(likeRPCContextKey{}) == "cancel-after-update" {
			tx.hooks.cancel()
		}
	}
	return result, err
}
func (tx *likeRPCTx) Commit() error {
	err := tx.Tx.Commit()
	if err == nil && tx.ctx.Value(likeRPCContextKey{}) == "commit-receipt" {
		tx.hooks.commitKnown.Store(true)
		return errLikeRPCCommitReceipt
	}
	return err
}

func likeRPCService(f *likeClampFixture, hooks *likeRPCHooks) *Service {
	pool := &likeRPCPool{DB: f.pool, hooks: hooks}
	f.db.Config.ConnPool, f.db.Statement.ConnPool = pool, pool
	return NewService(f.db)
}
func likeRPCState(t *testing.T, f *likeClampFixture, service *Service, wanted bool, version int, likes int64) {
	t.Helper()
	state, err := service.SetLike(f.ctx, strconv.FormatUint(f.reader, 10), strconv.FormatUint(f.note, 10), wanted)
	if err != nil || state.Liked != wanted || state.Version != version || state.Likes != likes {
		t.Fatalf("RPC Like result differs: %+v error=%v", state, err)
	}
}

// Taking both real row locks from a fresh transaction proves the previous
// mutation has ended; an unlocked consistent read alone could see old values
// while a canceled transaction still holds uncommitted changes.
func likeRPCProveRollback(t *testing.T, f *likeClampFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, err := f.pool.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var note, user, relation uint64
	var status string
	var active, version int
	if err := tx.QueryRowContext(ctx, lockedLikeFactsSQL, f.reader, f.note).Scan(&note, &user, &status, &relation, &active, &version); err != nil || note != f.note || relation == 0 || active != 1 || version != 1 {
		t.Fatal("fresh locking read did not prove rollback and released ownership")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	f.assertFacts(t, 1, 1, 1, 1)
}

type likeRPCGate struct {
	logger.Interface
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (gate *likeRPCGate) LogMode(level logger.LogLevel) logger.Interface {
	return &likeRPCGate{Interface: gate.Interface.LogMode(level), entered: gate.entered, release: gate.release}
}
func (gate *likeRPCGate) Trace(ctx context.Context, start time.Time, text func() (string, int64), err error) {
	if err == nil && ctx.Value(likeRPCContextKey{}) == "hold-read" {
		statement, _ := text()
		if strings.HasPrefix(statement, "SELECT STRAIGHT_JOIN b.id,b.user_id,b.status") {
			gate.once.Do(func() { close(gate.entered); <-gate.release })
		}
	}
	gate.Interface.Trace(ctx, start, text, err)
}

func likeRPCPointLocks(t *testing.T, f *likeClampFixture, service *Service) {
	t.Helper()
	f.pool.SetMaxOpenConns(12)
	f.pool.SetMaxIdleConns(12)
	result, err := f.pool.ExecContext(f.ctx, `INSERT INTO tb_blog(shop_id,user_id,title,images,content,liked,status,publish_version)
		VALUES (0,?,'RPC unrelated note','','owned lock probe',0,'PUBLISHED',1)`, f.author)
	if err != nil {
		t.Fatal(err)
	}
	extraID, err := result.LastInsertId()
	if err != nil || extraID <= 0 {
		t.Fatal("unrelated owned identity missing")
	}
	t.Logf("owned unrelated lock-probe note=%d", extraID)
	verified := false
	t.Cleanup(func() {
		if !verified {
			t.Log("unverified lock fixture retained")
			return
		}
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		r, err := f.pool.ExecContext(ctx, "DELETE FROM tb_blog WHERE id=? AND user_id=?", extraID, f.author)
		if err != nil {
			t.Error("unrelated exact lock fixture cleanup failed")
			return
		}
		if rows, err := r.RowsAffected(); err != nil || rows != 1 {
			t.Error("unrelated exact lock fixture cleanup rows differ")
		}
	})
	probe, err := f.pool.Conn(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	var oldTimeout int
	if err := probe.QueryRowContext(f.ctx, "SELECT @@SESSION.innodb_lock_wait_timeout").Scan(&oldTimeout); err != nil || oldTimeout < 1 || oldTimeout > 1073741824 {
		t.Fatal("session lock timeout unavailable")
	}
	if _, err := probe.ExecContext(f.ctx, "SET SESSION innodb_lock_wait_timeout=1"); err != nil {
		t.Fatal(err)
	}
	gate := &likeRPCGate{Interface: f.db.Logger, entered: make(chan struct{}), release: make(chan struct{})}
	f.db.Config.Logger = gate
	var release sync.Once
	unlock := func() { release.Do(func() { close(gate.release) }) }
	defer unlock()
	results := make(chan error, 8)
	call := func(ctx context.Context) {
		state, err := service.SetLike(ctx, strconv.FormatUint(f.reader, 10), strconv.FormatUint(f.note, 10), false)
		if err == nil && (state.Liked || state.Version != 2 || state.Likes != 0) {
			err = errors.New("concurrent Like typed result differs")
		}
		results <- err
	}
	go call(context.WithValue(f.ctx, likeRPCContextKey{}, "hold-read"))
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual joined lock read never entered gate")
	}
	// A server error1205 is an acknowledged lock-wait outcome, not an inference
	// from a sleeping goroutine or client elapsed time.
	for _, statement := range []string{"UPDATE tb_blog SET liked=liked WHERE id=?", "UPDATE tb_blog_like SET active=active WHERE blog_id=? AND user_id=?"} {
		args := []any{f.note}
		if strings.Contains(statement, "tb_blog_like") {
			args = append(args, f.reader)
		}
		_, err := probe.ExecContext(f.ctx, statement, args...)
		var mysqlError *mysqldriver.MySQLError
		if !errors.As(err, &mysqlError) || mysqlError.Number != 1205 {
			t.Fatalf("actual same-note/relation lock wait not proven: %v", err)
		}
	}
	independent, stop := context.WithTimeout(f.ctx, time.Second)
	r, err := probe.ExecContext(independent, "UPDATE tb_blog SET comments=comments+1 WHERE id=?", extraID)
	stop()
	if err != nil {
		t.Fatal("unrelated note blocked by joined point locks")
	}
	if rows, err := r.RowsAffected(); err != nil || rows != 1 {
		t.Fatal("unrelated note did not change exactly once")
	}
	for i := 0; i < 7; i++ {
		go call(f.ctx)
	}
	unlock()
	for i := 0; i < 8; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-f.ctx.Done():
			t.Fatal("concurrent Like calls failed to settle")
		}
	}
	f.assertFacts(t, 0, 2, 0, 2)
	// Restore only after all service calls are known and settled. On an
	// unexpected failure the connection/pool is closed and the fixture retained;
	// no deferred management SQL is issued against an uncertain live outcome.
	restore, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if _, err := probe.ExecContext(restore, fmt.Sprintf("SET SESSION innodb_lock_wait_timeout=%d", oldTimeout)); err != nil {
		t.Fatal("original session lock timeout restoration failed")
	}
	verified = true
}

func TestIntegrationLikeSQLRPC(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("registered isolated MySQL required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	if !regexp.MustCompile(`^cap10_relay_it_[a-f0-9]{12}$`).MatchString(cfg.MySQL.Database) {
		t.Skip("independent CAP10 fixture required before connecting")
	}
	for _, interpolation := range []bool{false, true} {
		if !t.Run(strconv.FormatBool(interpolation), func(t *testing.T) {
			for _, scenario := range []string{"lifecycle", "stale-fence", "cancel-after-update", "commit-receipt", "positive-overflow", "point-lock-concurrent"} {
				if !t.Run(scenario, func(t *testing.T) {
					local := cfg
					local.MySQL.InterpolateParams = interpolation
					f := newLikeClampFixture(t, local)
					hooks := &likeRPCHooks{}
					service := likeRPCService(f, hooks)
					reader, note := strconv.FormatUint(f.reader, 10), strconv.FormatUint(f.note, 10)
					if scenario == "lifecycle" {
						if _, err := service.SetLike(f.ctx, reader, strconv.FormatUint(f.note+1000000000, 10), true); !errors.Is(err, ErrNoteNotFound) {
							t.Fatal("missing note accepted")
						}
						if err := f.db.WithContext(f.ctx).Exec("UPDATE tb_blog SET status='DRAFT' WHERE id=?", f.note).Error; err != nil {
							t.Fatal(err)
						}
						if _, err := service.SetLike(f.ctx, reader, note, true); !errors.Is(err, ErrNoteNotFound) {
							t.Fatal("draft note accepted")
						}
						if err := f.db.WithContext(f.ctx).Exec("UPDATE tb_blog SET status='PUBLISHED' WHERE id=?", f.note).Error; err != nil {
							t.Fatal(err)
						}
						likeRPCState(t, f, service, false, 0, 0)
					}
					likeRPCState(t, f, service, true, 1, 1)
					f.assertFacts(t, 1, 1, 1, 1)
					switch scenario {
					case "stale-fence":
						_, err := service.SetLike(context.WithValue(f.ctx, likeRPCContextKey{}, "stale"), reader, note, false)
						if !errors.Is(err, ErrUnsafe) || hooks.updateRows.Load() != 0 {
							t.Fatal("actual zero-row version fence was not rejected")
						}
						likeRPCProveRollback(t, f)
					case "cancel-after-update":
						ctx, cancel := context.WithCancel(f.ctx)
						defer cancel()
						hooks.cancel = cancel
						_, err := service.SetLike(context.WithValue(ctx, likeRPCContextKey{}, "cancel-after-update"), reader, note, false)
						if !errors.Is(err, context.Canceled) || hooks.updateRows.Load() != 2 {
							t.Fatalf("real UPDATE/cancellation boundary differs: %v", err)
						}
						likeRPCProveRollback(t, f)
					case "commit-receipt":
						_, err := service.SetLike(context.WithValue(f.ctx, likeRPCContextKey{}, "commit-receipt"), reader, note, false)
						if !errors.Is(err, errLikeRPCCommitReceipt) || !hooks.commitKnown.Load() || hooks.postReadSeen.Load() != 0 {
							t.Fatal("commit error was converted into a successful response or post-read")
						}
						f.assertFacts(t, 0, 2, 0, 2)
						f.cleanupVerified = true
						return
					case "point-lock-concurrent":
						likeRPCPointLocks(t, f, service)
						f.cleanupVerified = true
						return
					case "lifecycle":
						if err := f.db.WithContext(f.ctx).Exec("UPDATE tb_blog SET status='DRAFT' WHERE id=?", f.note).Error; err != nil {
							t.Fatal(err)
						}
						if _, err := service.SetLike(f.ctx, reader, note, false); !errors.Is(err, ErrNoteNotFound) {
							t.Fatal("draft with an existing relation accepted")
						}
						if err := f.db.WithContext(f.ctx).Exec("UPDATE tb_blog SET status='PUBLISHED' WHERE id=?", f.note).Error; err != nil {
							t.Fatal(err)
						}
						f.assertFacts(t, 1, 1, 1, 1)
						likeRPCState(t, f, service, true, 1, 1)
					}
					// A fresh locking read has already proven rollback in the two
					// injected failure cases; this is a new known-state transition,
					// not a blind retry of an uncertain request.
					likeRPCState(t, f, service, false, 2, 0)
					likeRPCState(t, f, service, false, 2, 0)
					f.assertFacts(t, 0, 2, 0, 2)
					if scenario == "positive-overflow" {
						if err := f.db.WithContext(f.ctx).Exec("UPDATE tb_blog SET liked=4294967295 WHERE id=?", f.note).Error; err != nil {
							t.Fatal(err)
						}
						_, err := service.SetLike(f.ctx, reader, note, true)
						var mysqlError *mysqldriver.MySQLError
						if !errors.As(err, &mysqlError) || mysqlError.Number != 1264 {
							t.Fatal("positive overflow did not remain an error")
						}
						f.assertFacts(t, 0, 2, 4294967295, 2)
					}
					f.cleanupVerified = true
				}) {
					return
				}
			}
		}) {
			return
		}
	}
}
