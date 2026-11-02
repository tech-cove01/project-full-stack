package communityrelay

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// Pause the real SELECT after its rows have been scanned, before Claim updates
// the selected row and commits. This controls the lock lifetime without replacing
// Claim's SQL or implementing a second repository just for the test.
type claimScanGate struct {
	logger.Interface
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
	closeOnce sync.Once
}

func (gate *claimScanGate) Trace(ctx context.Context, _ time.Time, query func() (string, int64), err error) {
	text, _ := query()
	if err == nil && strings.Contains(text, "idx_community_active_claim") && strings.Contains(text, "FOR UPDATE SKIP LOCKED") {
		gate.once.Do(func() {
			close(gate.entered)
			select {
			case <-gate.release:
			case <-ctx.Done():
			}
		})
	}
}

func (gate *claimScanGate) resume() { gate.closeOnce.Do(func() { close(gate.release) }) }

// Claim is global, so this regression requires the registered independent CAP10
// integration database and an empty outbox. It never touches the capacity DB.
func TestIntegrationClaimDoesNotLockLiveLeaseWhileSelectingDueTail(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	if !strings.HasPrefix(cfg.MySQL.Database, "cap10_relay_it_") {
		t.Skip("registered independent CAP10 integration database is required for the global claim lock fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	var existing int64
	if err = r.ORM.WithContext(ctx).Raw("SELECT COUNT(*) FROM tb_community_outbox").Scan(&existing).Error; err != nil || existing != 0 {
		t.Fatal("claim lock regression requires an empty isolated outbox; existing rows untouched")
	}
	for _, isolation := range []string{"REPEATABLE READ", "READ COMMITTED"} {
		t.Run(isolation, func(t *testing.T) {
			prefix := "CLAIM-LOCK:" + randomSuffix(t)
			ids := []string{prefix + ":live", prefix + ":due"}
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if err := r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_community_outbox WHERE event_id IN ?", ids).Error; err != nil {
					t.Error("owned lock fixture cleanup failed")
				}
			})
			for _, id := range ids {
				if err := r.ORM.WithContext(ctx).Exec(`INSERT INTO tb_community_outbox
 (event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload)
 VALUES (?,'FOLLOW','42',1,'FOLLOW_CHANGED','{}')`, id).Error; err != nil {
					t.Fatal("owned lock fixture insert failed")
				}
			}
			if err := r.ORM.WithContext(ctx).Exec(`UPDATE tb_community_outbox SET delivery_status='PROCESSING',
 attempts=1,lease_until=DATE_ADD(NOW(3),INTERVAL 1 HOUR) WHERE event_id=?`, ids[0]).Error; err != nil {
				t.Fatal("owned live lease setup failed")
			}
			var head Event
			if err := r.ORM.WithContext(ctx).Raw("SELECT id,event_id,attempts FROM tb_community_outbox WHERE event_id=?", ids[0]).Scan(&head).Error; err != nil || head.ID == 0 {
				t.Fatal("owned live lease not found")
			}
			gate := &claimScanGate{Interface: logger.Default.LogMode(logger.Silent), entered: make(chan struct{}), release: make(chan struct{})}
			defer gate.resume()
			type claimResult struct {
				event *Event
				err   error
			}
			claimDone := make(chan claimResult, 1)
			go func() {
				var event *Event
				err := r.ORM.WithContext(ctx).Connection(func(connection *gorm.DB) error {
					if err := connection.Exec("SET SESSION TRANSACTION ISOLATION LEVEL " + isolation).Error; err != nil {
						return err
					}
					defer connection.Exec("SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ")
					var claimErr error
					event, claimErr = NewRepository(connection.Session(&gorm.Session{Logger: gate})).Claim(ctx)
					return claimErr
				})
				claimDone <- claimResult{event: event, err: err}
			}()
			select {
			case <-gate.entered:
			case result := <-claimDone:
				t.Fatalf("Claim ended before actual SELECT gate: %v", result.err)
			case <-time.After(3 * time.Second):
				t.Fatal("actual claim SELECT did not reach the trace gate")
			}
			doneResult := make(chan error, 1)
			started := time.Now()
			go func() { doneResult <- NewRepository(r.ORM).Done(ctx, head) }()
			completedBeforeRelease := false
			select {
			case err := <-doneResult:
				if err != nil {
					t.Fatalf("independent owner Done failed: %v", err)
				}
				completedBeforeRelease = true
				t.Logf("session=%s live-head Done completed while due-tail Claim SELECT remains paused; elapsed=%s", isolation, time.Since(started))
			case <-time.After(500 * time.Millisecond):
				t.Errorf("session=%s nonmatching live lease blocked owner Done for500ms while due-tail Claim SELECT was paused", isolation)
			}
			gate.resume()
			result := <-claimDone
			if result.err != nil || result.event == nil || result.event.EventID != ids[1] || result.event.Attempts != 1 {
				t.Fatalf("actual Claim did not preserve due-tail lease/fence: event=%+v err=%v", result.event, result.err)
			}
			if !completedBeforeRelease {
				if err := <-doneResult; err != nil {
					t.Fatalf("Done failed after Claim transaction release: %v", err)
				}
				t.Logf("session=%s blocked Done recovered only after Claim release; elapsed=%s", isolation, time.Since(started))
			}
			if err := NewRepository(r.ORM).Done(ctx, *result.event); err != nil {
				t.Fatal("selected due tail did not retain its completion fence")
			}
		})
	}
}
