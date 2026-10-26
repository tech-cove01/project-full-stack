package ordertimeout

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func integrationDatabase(t *testing.T) (*gorm.DB, config.Config) {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MYSQL_* configuration")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot initialize isolated MySQL")
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal("cannot access isolated MySQL")
	}
	pool.SetMaxOpenConns(10)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if pool.PingContext(ctx) != nil {
		t.Fatal("isolated MySQL unavailable")
	}
	t.Cleanup(func() {
		if pool.Close() != nil {
			t.Error("cannot close integration MySQL")
		}
	})
	connection, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal("cannot reserve integration lock connection")
	}
	var acquired int
	if connection.QueryRowContext(ctx, "SELECT GET_LOCK('go031_integration',30)").Scan(&acquired) != nil || acquired != 1 {
		_ = connection.Close()
		t.Fatal("GO031 integration window unavailable")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released int
		if connection.QueryRowContext(ctx, "SELECT RELEASE_LOCK('go031_integration')").Scan(&released) != nil || released != 1 {
			t.Error("cannot release GO031 integration lock")
		}
		_ = connection.Close()
	})
	var existing int64
	if db.Raw("SELECT COUNT(*) FROM tb_voucher_order WHERE status=1 AND pay_deadline_at<=NOW()").Scan(&existing).Error != nil || existing != 0 {
		t.Fatal("finish other owned due fixtures before full-table timeout scanning tests")
	}
	return db, cfg
}

type sqlFixture struct {
	db    *gorm.DB
	base  int64
	now   time.Time
	ids   []int64
	owned map[string]bool
}

func newSQLFixture(t *testing.T, db *gorm.DB) *sqlFixture {
	t.Helper()
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("cannot allocate timeout fixture")
	}
	base := int64(9_200_000_000_000_000 + binary.LittleEndian.Uint64(entropy[:])%1_000_000_000_000)
	f := &sqlFixture{db: db, base: base, owned: map[string]bool{}}
	var existing int64
	if db.Raw("SELECT COUNT(*) FROM tb_voucher_order WHERE id BETWEEN ? AND ?", base, base+300).Scan(&existing).Error != nil || existing != 0 {
		t.Fatal("cannot establish exact fixture ID ownership")
	}
	if db.Raw("SELECT NOW() AS value").Scan(&f.now).Error != nil || f.now.IsZero() {
		t.Fatal("cannot read DB fixture clock")
	}
	t.Cleanup(func() {
		if len(f.ids) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if db.WithContext(ctx).Exec("DELETE FROM tb_voucher_order WHERE id IN ? AND voucher_id=?", f.ids, base).Error != nil {
			t.Error("cannot delete exact timeout fixture orders")
		}
		var remaining int64
		if db.WithContext(ctx).Raw("SELECT COUNT(*) FROM tb_voucher_order WHERE id IN ?", f.ids).Scan(&remaining).Error != nil || remaining != 0 {
			t.Error("timeout cleanup left owned orders")
		}
	})
	return f
}

func (f *sqlFixture) add(t *testing.T, status int, deadline time.Time) string {
	t.Helper()
	id := f.base + int64(len(f.ids)) + 1
	if err := f.db.Exec(`INSERT INTO tb_voucher_order
		(id,user_id,voucher_id,shop_id,pay_type,pay_amount,status,pay_deadline_at,version)
		VALUES(?,?,?,?,1,990,?,?,0)`, id, id, f.base, f.base, status, deadline).Error; err != nil {
		t.Fatal("cannot insert owned timeout fixture")
	}
	f.ids = append(f.ids, id)
	text := strconv.FormatInt(id, 10)
	f.owned[text] = true
	return text
}

// This controlled processor intentionally tests only scan/scheduling behavior.
// It does not implement inventory, audit, outbox, authorization or domain locks.
func (f *sqlFixture) cancelSQL(ctx context.Context, id string) (bool, error) {
	if !f.owned[id] {
		return false, errors.New("refusing to mutate an unowned timeout row")
	}
	result := f.db.WithContext(ctx).Exec(`UPDATE tb_voucher_order SET status=4,version=version+1
		WHERE id=? AND voucher_id=? AND status=1 AND pay_deadline_at<=NOW()`, id, f.base)
	return result.RowsAffected == 1, result.Error
}

func (f *sqlFixture) row(t *testing.T, id string) (status, version int) {
	t.Helper()
	var row struct{ Status, Version int }
	if f.db.Raw("SELECT status,version FROM tb_voucher_order WHERE id=? AND voucher_id=?", id, f.base).Scan(&row).Error != nil {
		t.Fatal("cannot read owned timeout row")
	}
	return row.Status, row.Version
}

func TestIntegrationTimeoutScan(t *testing.T) {
	db, cfg := integrationDatabase(t)
	ctx := context.Background()
	t.Run("db_clock_status_and_deadline_id_order_select_only_due_unpaid_rows", func(t *testing.T) {
		f := newSQLFixture(t, db)
		late := f.add(t, 1, f.now.Add(-time.Second))
		early := f.add(t, 1, f.now.Add(-time.Minute))
		tie := f.add(t, 1, f.now.Add(-time.Minute))
		future := f.add(t, 1, f.now.Add(time.Hour))
		paid := f.add(t, 2, f.now.Add(-time.Minute))
		cancelled := f.add(t, 4, f.now.Add(-time.Minute))
		refunded := f.add(t, 6, f.now.Add(-time.Minute))
		boundary := f.add(t, 1, f.now)
		var called []string
		worker := NewWorker(db, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
			called = append(called, id)
			return f.cancelSQL(ctx, id)
		}))
		count, err := worker.Poll(ctx)
		if err != nil || count != 4 || !reflect.DeepEqual(called, []string{early, tie, late, boundary}) {
			t.Fatal("SQL scan did not use DB due predicate and stable deadline/ID ordering")
		}
		for _, id := range called {
			if status, version := f.row(t, id); status != 4 || version != 1 {
				t.Fatal("owned due order was not conditionally updated")
			}
		}
		for id, want := range map[string]int{future: 1, paid: 2, cancelled: 4, refunded: 6} {
			if status, version := f.row(t, id); status != want || version != 0 {
				t.Fatal("scanner processed future or non-unpaid order")
			}
		}
		t.Logf("scanClock=DB NOW() orderIds=%v attempted=%d untouchedFutureAndTerminal=4", called, count)
	})
	t.Run("one_hundred_failed_rows_do_not_starve_later_due_order_and_are_retried", func(t *testing.T) {
		f := newSQLFixture(t, db)
		var ids []string
		for index := 0; index < 101; index++ {
			ids = append(ids, f.add(t, 1, f.now.Add(-time.Minute)))
		}
		var called []string
		worker := NewWorker(db, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
			called = append(called, id)
			if id != ids[100] {
				return false, errors.New("owned transient error")
			}
			return f.cancelSQL(ctx, id)
		}))
		if count, err := worker.Poll(ctx); count != 100 || err == nil {
			t.Fatal("first production-size failure page was not attempted")
		}
		if count, err := worker.Poll(ctx); count != 1 || err != nil {
			t.Fatal("later due row was starved by the first failed page")
		}
		if status, version := f.row(t, ids[100]); status != 4 || version != 1 {
			t.Fatal("later due row was not processed")
		}
		if count, err := worker.Poll(ctx); count != 100 || err == nil || len(called) != 201 {
			t.Fatal("earlier failures were not retained and revisited")
		}
		for _, id := range ids[:100] {
			if status, version := f.row(t, id); status != 1 || version != 0 {
				t.Fatal("failed row lost its DB retry source")
			}
		}
		t.Logf("batch=100 failedFront=100 laterProcessed=%s calls=100,1,100 retrySource=DB", ids[100])
	})
	t.Run("lifecycle_rechecks_state_after_selection_and_false_is_not_an_error", func(t *testing.T) {
		f := newSQLFixture(t, db)
		id := f.add(t, 1, f.now.Add(-time.Minute))
		worker := NewWorker(db, lifecycleFunc(func(ctx context.Context, received string) (bool, error) {
			if received != id {
				return false, errors.New("unowned row")
			}
			if err := db.WithContext(ctx).Exec("UPDATE tb_voucher_order SET status=2 WHERE id=? AND voucher_id=?", id, f.base).Error; err != nil {
				return false, err
			}
			return f.cancelSQL(ctx, received)
		}))
		if count, err := worker.Poll(ctx); count != 1 || err != nil {
			t.Fatal("another action winning must be a safe attempted no-op")
		}
		if status, version := f.row(t, id); status != 2 || version != 0 {
			t.Fatal("scanner overrode the newer paid state")
		}
	})
	t.Run("two_scanners_can_overlap_and_conditional_processor_changes_each_row_once", func(t *testing.T) {
		f := newSQLFixture(t, db)
		for index := 0; index < 12; index++ {
			f.add(t, 1, f.now.Add(-time.Minute))
		}
		arrived := make(chan struct{}, 2)
		release := make(chan struct{})
		var successes atomic.Int32
		processor := func(first *atomic.Bool) lifecycleFunc {
			return func(ctx context.Context, id string) (bool, error) {
				if first.CompareAndSwap(false, true) {
					arrived <- struct{}{}
					select {
					case <-release:
					case <-ctx.Done():
						return false, ctx.Err()
					}
				}
				changed, err := f.cancelSQL(ctx, id)
				if changed {
					successes.Add(1)
				}
				return changed, err
			}
		}
		var first, second atomic.Bool
		workers := []*Worker{NewWorker(db, processor(&first)), NewWorker(db, processor(&second))}
		var wait sync.WaitGroup
		counts := make([]int, 2)
		failures := make([]error, 2)
		for index, worker := range workers {
			wait.Add(1)
			go func(index int, worker *Worker) { defer wait.Done(); counts[index], failures[index] = worker.Poll(ctx) }(index, worker)
		}
		for index := 0; index < 2; index++ {
			select {
			case <-arrived:
			case <-time.After(time.Second):
				close(release)
				wait.Wait()
				t.Fatal("both scanners did not select the shared due page")
			}
		}
		close(release)
		wait.Wait()
		if failures[0] != nil || failures[1] != nil || counts[0]+counts[1] != 24 || successes.Load() != 12 {
			t.Fatal("overlapping scans were mistaken for duplicate successful cancellations")
		}
		for id := range f.owned {
			if status, version := f.row(t, id); status != 4 || version != 1 {
				t.Fatal("overlap produced more than one conditional state transition")
			}
		}
	})
	t.Run("scan_pool_failure_retains_position_and_healthy_pool_recovers", func(t *testing.T) {
		f := newSQLFixture(t, db)
		id := f.add(t, 1, f.now.Add(-time.Minute))
		closed, err := gorm.Open(gormmysql.New(gormmysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal("cannot arrange independent scan failure")
		}
		pool, err := closed.DB()
		if err != nil || pool.Close() != nil {
			t.Fatal("cannot close independent scan pool")
		}
		worker := NewWorker(closed, lifecycleFunc(f.cancelSQL))
		worker.position = cursor{PayDeadlineAt: f.now.Add(-2 * time.Minute), ID: f.base}
		before := worker.position
		if count, err := worker.Poll(ctx); count != 0 || err == nil || strings.Contains(err.Error(), "sql:") || worker.position != before {
			t.Fatal("scan failure exposed driver text or changed recovery position")
		}
		worker.db = db
		if count, err := worker.Poll(ctx); count != 1 || err != nil {
			t.Fatal("healthy DB did not recover the unprocessed due row")
		}
		if status, version := f.row(t, id); status != 4 || version != 1 {
			t.Fatal("scan failure lost its persistent retry source")
		}
	})
	t.Run("run_scans_on_start_without_any_redis_hint_and_cancels_cleanly", func(t *testing.T) {
		f := newSQLFixture(t, db)
		id := f.add(t, 1, f.now.Add(-time.Minute))
		changed := make(chan struct{}, 1)
		worker := NewWorker(db, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
			done, err := f.cancelSQL(ctx, id)
			if done {
				changed <- struct{}{}
			}
			return done, err
		}))
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		finished := make(chan struct{})
		go func() { worker.Run(runCtx); close(finished) }()
		select {
		case <-changed:
		case <-time.After(time.Second):
			t.Fatal("startup did not immediately scan the DB due order")
		}
		cancel()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("timeout Run did not exit on application cancellation")
		}
		if status, version := f.row(t, id); status != 4 || version != 1 {
			t.Fatal("DB-only startup scan did not process its order")
		}
		t.Logf("redisClient=none zsetHint=none dueOrder=%s startupScan=immediate cancelled=true", id)
	})
}
