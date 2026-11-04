package communityrelay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type donePointITState struct {
	ID                          uint64
	Status                      string
	Attempts                    int
	Lease, Completed, LastError sql.NullString
}

type donePointITFixture struct {
	db     *gorm.DB
	ctx    context.Context
	events []Event
	ids    []string
}

func donePointITConfig(t *testing.T) config.Config {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("registered isolated MySQL required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	if !regexp.MustCompile(`^cap10_relay_it_[a-f0-9]{12}$`).MatchString(cfg.MySQL.Database) {
		t.Skip("independent CAP10 database required before connecting")
	}
	return cfg
}

// This fixture uses only MySQL. It neither opens Redis/Kafka nor truncates a
// table, and registers every owned ID before its atomic insert transaction.
func newDonePointIT(t *testing.T, cfg config.Config, size int) *donePointITFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("isolated MySQL unavailable")
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(4)
	pool.SetMaxIdleConns(4)
	t.Cleanup(func() { _ = pool.Close() })
	var count int64
	if err = db.WithContext(ctx).Raw("SELECT COUNT(*) FROM tb_community_outbox").Scan(&count).Error; err != nil || count != 0 {
		t.Fatal("empty independent outbox required; existing rows untouched")
	}
	f := &donePointITFixture{db: db.WithContext(ctx), ctx: ctx}
	prefix := "DONE-POINT-IT:" + randomSuffix(t)
	for index := 0; index < size; index++ {
		f.ids = append(f.ids, fmt.Sprintf("%s:%d", prefix, index))
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := db.WithContext(cleanup).Exec("DELETE FROM tb_community_outbox WHERE event_id IN ?", f.ids).Error; err != nil {
			t.Error("exact owned Done point fixture cleanup failed")
		}
	})
	if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range f.ids {
			if err := tx.Exec(`INSERT INTO tb_community_outbox
 (event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload,delivery_status,attempts,lease_until,last_error)
 VALUES (?,'FOLLOW','42',1,'FOLLOW_CHANGED','{}','PROCESSING',1,DATE_ADD(NOW(3),INTERVAL 1 HOUR),'owned-before')`, id).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Raw("SELECT id,event_id,attempts FROM tb_community_outbox WHERE event_id IN ? ORDER BY id", f.ids).Scan(&f.events).Error; err != nil || len(f.events) != size {
		t.Fatal("owned Done point identity lookup failed")
	}
	return f
}

func (f *donePointITFixture) states(t *testing.T) []donePointITState {
	t.Helper()
	var rows []donePointITState
	if err := f.db.Raw(`SELECT id,delivery_status AS status,attempts,
 DATE_FORMAT(lease_until,'%Y-%m-%d %H:%i:%s.%f') AS lease,
 DATE_FORMAT(completed_at,'%Y-%m-%d %H:%i:%s.%f') AS completed,last_error
 FROM tb_community_outbox WHERE event_id IN ? ORDER BY id`, f.ids).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestIntegrationDonePointBatchAtomicFence(t *testing.T) {
	cfg := donePointITConfig(t)
	for _, size := range []int{32, 8} {
		t.Run(fmt.Sprintf("N%d", size), func(t *testing.T) {
			f := newDonePointIT(t, cfg, size)
			before := f.states(t)
			input := append([]Event(nil), f.events...)
			if size == 8 {
				input[size/2].Attempts++
			}
			err := NewRepository(f.db).DoneBatch(f.ctx, input)
			if size == 8 {
				if !errors.Is(err, ErrLeaseLost) || !reflect.DeepEqual(before, f.states(t)) {
					t.Fatal("short paired fence did not roll back every field")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range f.states(t) {
				if row.Status != "DONE" || row.Attempts != 1 || row.Lease.Valid || !row.Completed.Valid || row.LastError.Valid {
					t.Fatal("batch completion differs from original Done state")
				}
			}
		})
	}
}

// Trace pauses after the actual UPDATE response, never before sending its SQL.
type donePointITUpdateGate struct {
	logger.Interface
	entered, release chan struct{}
	once, closeOnce  sync.Once
	rows             int64
}

func (gate *donePointITUpdateGate) Trace(ctx context.Context, _ time.Time, query func() (string, int64), err error) {
	text, rows := query()
	if err == nil && strings.Contains(text, "SET o.delivery_status='DONE'") {
		gate.once.Do(func() {
			gate.rows = rows
			close(gate.entered)
			select {
			case <-gate.release:
			case <-ctx.Done():
			}
		})
	}
}
func (gate *donePointITUpdateGate) resume() { gate.closeOnce.Do(func() { close(gate.release) }) }

func TestIntegrationDonePointBatchFreshExpiryGuard(t *testing.T) {
	f := newDonePointIT(t, donePointITConfig(t), 8)
	if err := f.db.Exec("UPDATE tb_community_outbox SET lease_until=DATE_ADD(NOW(3),INTERVAL 400000 MICROSECOND) WHERE id=?", f.events[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	before := f.states(t)
	ctx, cancel := context.WithTimeout(f.ctx, 6*time.Second)
	gate := &donePointITUpdateGate{Interface: logger.Default.LogMode(logger.Silent), entered: make(chan struct{}), release: make(chan struct{})}
	defer func() { cancel(); gate.resume() }()
	result := make(chan error, 1)
	go func() { result <- NewRepository(f.db.Session(&gorm.Session{Logger: gate})).DoneBatch(ctx, f.events) }()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual update response did not reach the pause")
	}
	if gate.rows != 8 {
		t.Fatal("pause did not follow a full successful UPDATE")
	}
	expired := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := f.db.Raw("SELECT lease_until<=NOW(3) FROM tb_community_outbox WHERE id=?", f.events[0].ID).Row().Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !expired {
		t.Fatal("server did not confirm original lease expiry")
	}
	gate.resume()
	select {
	case err := <-result:
		if !errors.Is(err, ErrLeaseLost) || !reflect.DeepEqual(before, f.states(t)) {
			t.Fatal("fresh guard did not roll back all actual completed rows")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("candidate did not leave the actual fresh guard")
	}
}
