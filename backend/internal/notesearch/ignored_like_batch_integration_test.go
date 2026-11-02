package notesearch

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"gorm.io/gorm"
)

type ignoredLikeNoES struct{ calls atomic.Int32 }

func (transport *ignoredLikeNoES) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("ignored LIKE batch must not contact Elasticsearch")
}

// This test requires an empty isolated receipt table, changes only its exact
// event ids, and never starts Kafka or contacts ES. The control-row deletion
// case is confined to an explicitly rolled-back SQL transaction.
func TestIntegrationSearchIgnoredLikesBatch(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated MySQL and Redis required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	connection, err := r.SQL.Conn(ctx)
	if err != nil {
		t.Fatal("search integration lock unavailable")
	}
	var acquired int
	if connection.QueryRowContext(ctx, "SELECT GET_LOCK('go076_integration',10)").Scan(&acquired) != nil || acquired != 1 {
		_ = connection.Close()
		t.Fatal("another search integration fixture is active")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = connection.ExecContext(cleanup, "SELECT RELEASE_LOCK('go076_integration')")
		_ = connection.Close()
	})
	var count int64
	if err := r.ORM.WithContext(ctx).Raw("SELECT COUNT(*) FROM tb_community_projection_event").Scan(&count).Error; err != nil || count != 0 {
		t.Fatal("ignored-batch test requires an empty isolated receipt table; existing rows untouched")
	}
	var control searchControl
	if err := r.ORM.WithContext(ctx).Raw("SELECT name,index_alias,active_generation,rebuilt_at FROM tb_note_search_control WHERE name=?", Projection).Scan(&control).Error; err != nil || control.Name != Projection {
		t.Fatal("search control migration is required")
	}
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("fixture entropy unavailable")
	}
	seed := uint64(1000000000000) + binary.BigEndian.Uint64(entropy[:])%1000000000
	messages := make([]communityprojection.Message, 34)
	ids := make([]string, len(messages))
	for i := range messages {
		messages[i] = ignoredLikeMessage(fmt.Sprint(seed+uint64(i)), "18", 1, true)
		event, err := decodeSearchMessage(Projection, messages[i])
		if err != nil {
			t.Fatal("fixture source invalid")
		}
		ids[i] = event.EventID
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_community_projection_event WHERE projection=? AND event_id IN ?", Projection, ids).Error; err != nil {
			t.Error("owned ignored receipts cleanup failed")
		}
	})
	transport := &ignoredLikeNoES{}
	runtime := NewSync(r.ORM, &Client{index: "go076-ignored-batch", http: &http.Client{Transport: transport}})
	ownedCount := func(want int64) {
		t.Helper()
		var got int64
		if err := r.ORM.WithContext(ctx).Raw("SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id IN ?", Projection, ids).Scan(&got).Error; err != nil || got != want {
			t.Fatalf("owned receipt count differs: got=%d want=%d", got, want)
		}
	}
	for i := 0; i < 2; i++ {
		if err := runtime.ApplyIgnoredLikes(ctx, Projection, messages[:32]); err != nil {
			t.Fatalf("initial/replayed ignored batch failed: %T", err)
		}
	}
	ownedCount(32)
	conflicting := ignoredLikeMessage(fmt.Sprint(seed), "18", 1, false)
	if err := runtime.ApplyIgnoredLikes(ctx, Projection, []communityprojection.Message{messages[32], conflicting}); !errors.Is(err, ErrSyncConflict) {
		t.Fatal("existing hash conflict must roll back every new receipt in the batch")
	}
	ownedCount(32)
	if err := r.ORM.Exec("UPDATE tb_community_projection_event SET state='APPLIED' WHERE projection=? AND event_id=?", Projection, ids[1]).Error; err != nil {
		t.Fatal(err)
	}
	if err := runtime.ApplyIgnoredLikes(ctx, Projection, []communityprojection.Message{messages[32], messages[1]}); !errors.Is(err, ErrSyncConflict) {
		t.Fatal("existing state conflict must roll back every new receipt")
	}
	ownedCount(32)
	if err := r.ORM.Exec("UPDATE tb_community_projection_event SET state='IGNORED' WHERE projection=? AND event_id=?", Projection, ids[1]).Error; err != nil {
		t.Fatal(err)
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	if err := runtime.ApplyIgnoredLikes(stopped, Projection, messages[32:33]); err == nil {
		t.Fatal("cancelled transaction cannot acknowledge receipt durability")
	}
	ownedCount(32)
	locked := r.ORM.WithContext(ctx).Begin()
	if locked.Error != nil {
		t.Fatal(locked.Error)
	}
	defer func() { _ = locked.Rollback().Error }()
	if _, err := lockSearchControl(locked); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runtime.ApplyIgnoredLikes(ctx, Projection, messages[33:]) }()
	select {
	case <-finished:
		t.Fatal("ignored batch bypassed the rebuild control row lock")
	case <-time.After(100 * time.Millisecond):
	}
	if err := locked.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal("ignored batch failed after the control lock was released")
		}
	case <-ctx.Done():
		t.Fatal("waiting ignored batch did not finish")
	}
	ownedCount(33)
	rollback := errors.New("restore isolated search control after missing-control case")
	err = r.ORM.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM tb_note_search_control WHERE name=?", Projection).Error; err != nil {
			return err
		}
		if err := NewSync(tx, runtime.client).ApplyIgnoredLikes(ctx, Projection, messages[32:33]); !errors.Is(err, ErrNeedsRebuild) {
			return errors.New("missing search control accepted ignored receipts")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("missing-control case did not restore the isolated barrier")
	}
	ownedCount(33)
	if transport.calls.Load() != 0 {
		t.Fatal("ignored LIKE receipts touched Elasticsearch")
	}
}
