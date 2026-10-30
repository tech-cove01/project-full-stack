package taildiag_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
	"gorm.io/gorm"
)

// The observer wraps GORM's transaction pool interface. Exercise that interface
// against actual dependencies before using its durations as diagnostic evidence.
// No shared business rows, cache keys or dependency settings are changed.
func TestIntegrationObservedStorageTransactions(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	taildiag.Instrument(resources.ORM, resources.SQL, resources.Redis)
	recorder := taildiag.New()
	observed, finish := taildiag.Start(ctx, recorder, false)
	marker := errors.New("controlled callback rollback")
	for _, rollback := range []bool{false, true} {
		err = resources.ORM.WithContext(observed).Transaction(func(tx *gorm.DB) error {
			var one int
			if queryErr := tx.Raw("SELECT 1").Scan(&one).Error; queryErr != nil || one != 1 {
				t.Fatal("observed read-only transaction query failed")
			}
			if rollback {
				return marker
			}
			return nil
		}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if (rollback && !errors.Is(err, marker)) || (!rollback && err != nil) {
			t.Fatal("observation changed transaction completion")
		}
		if resources.SQL.Stats().InUse != 0 {
			t.Fatal("completed observed transaction retained a connection")
		}
	}
	if err = resources.Redis.Ping(observed).Err(); err != nil {
		t.Fatal("observed Redis command failed")
	}
	canceled, stop := context.WithCancel(observed)
	stop()
	if err = resources.Redis.Ping(canceled).Err(); !errors.Is(err, context.Canceled) {
		t.Fatal("Redis observation changed caller cancellation")
	}
	finish(false)
	seen := make(map[string]uint64)
	for _, stage := range recorder.Snapshot().Stages {
		seen[stage.Name] = stage.Count
		if stage.Active != 0 {
			t.Fatalf("completed stage retained active observation: %s", stage.Name)
		}
	}
	if seen["sql.begin_acquire_roundtrip"] != 2 || seen["sql.transaction_hold"] != 2 ||
		seen["sql.statement_total"] != 2 || seen["redis.command_total"] != 2 {
		t.Fatalf("real dependency boundaries were not recorded: %+v", seen)
	}
	if pool, dbErr := resources.ORM.DB(); dbErr != nil || pool != resources.SQL {
		t.Fatal("observed ORM no longer exposes its original safe pool")
	}
}
