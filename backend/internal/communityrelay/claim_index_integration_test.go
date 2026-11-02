package communityrelay

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// Claim is intentionally global: even with exact fixture cleanup this test must
// never run against the capacity database. Require an empty isolated outbox.
func TestIntegrationActiveClaimPreservesEligibilityAndSkipLocked(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	var existing int64
	if err = r.ORM.WithContext(ctx).Raw("SELECT COUNT(*) FROM tb_community_outbox").Scan(&existing).Error; err != nil {
		t.Fatal(err)
	}
	if existing != 0 {
		t.Fatal("active-claim integration requires an empty isolated outbox; existing rows untouched")
	}
	prefix := "CLAIM-INDEX:" + randomSuffix(t)
	names := []string{"done", "review", "future", "live", "expired", "locked", "next"}
	ids := make([]string, len(names))
	for i, name := range names {
		ids[i] = prefix + ":" + name
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_community_outbox WHERE event_id IN ?", ids).Error; err != nil {
			t.Error("owned outbox fixture cleanup failed")
		}
	})
	for _, id := range ids {
		if err = r.ORM.WithContext(ctx).Exec(`INSERT INTO tb_community_outbox
			(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload)
			VALUES (?,'FOLLOW','42',1,'FOLLOW_CHANGED','{}')`, id).Error; err != nil {
			t.Fatal("outbox fixture insert failed")
		}
	}
	for _, update := range []struct {
		query string
		id    string
	}{
		{`UPDATE tb_community_outbox SET delivery_status='DONE' WHERE event_id=?`, ids[0]},
		{`UPDATE tb_community_outbox SET delivery_status='NEEDS_REVIEW' WHERE event_id=?`, ids[1]},
		{`UPDATE tb_community_outbox SET next_attempt_at=DATE_ADD(NOW(3),INTERVAL 1 HOUR) WHERE event_id=?`, ids[2]},
		{`UPDATE tb_community_outbox SET delivery_status='PROCESSING',attempts=1,lease_until=DATE_ADD(NOW(3),INTERVAL 1 HOUR) WHERE event_id=?`, ids[3]},
		{`UPDATE tb_community_outbox SET delivery_status='PROCESSING',attempts=2,lease_until=DATE_SUB(NOW(3),INTERVAL 1 SECOND) WHERE event_id=?`, ids[4]},
	} {
		if err = r.ORM.WithContext(ctx).Exec(update.query, update.id).Error; err != nil {
			t.Fatal("outbox eligibility setup failed")
		}
	}
	var flags []struct {
		EventID     string
		ClaimActive int
	}
	if err = r.ORM.WithContext(ctx).Raw("SELECT event_id,claim_active FROM tb_community_outbox ORDER BY id").Scan(&flags).Error; err != nil {
		t.Fatal("active-claim migration required")
	}
	if len(flags) != len(ids) {
		t.Fatal("isolated claim fixture unexpectedly changed")
	}
	for i, row := range flags {
		want := 1
		if i < 2 {
			want = 0
		}
		if row.EventID != ids[i] || row.ClaimActive != want {
			t.Fatal("generated active bucket differs from delivery state")
		}
	}
	repo := NewRepository(r.ORM)
	first, err := repo.Claim(ctx)
	if err != nil || first == nil || first.EventID != ids[4] || first.Attempts != 3 {
		t.Fatalf("earliest due expired lease must be reclaimed: event=%+v err=%v", first, err)
	}
	if err = repo.Done(ctx, *first); err != nil {
		t.Fatal("reclaimed lease completion failed")
	}
	var active int
	if err = r.ORM.WithContext(ctx).Raw("SELECT claim_active FROM tb_community_outbox WHERE event_id=?", ids[4]).Scan(&active).Error; err != nil || active != 0 {
		t.Fatal("completed row must leave the active bucket")
	}
	locked := r.ORM.WithContext(ctx).Begin()
	if locked.Error != nil {
		t.Fatal(locked.Error)
	}
	defer func() { _ = locked.Rollback().Error }()
	var lockedID uint64
	if err = locked.Raw("SELECT id FROM tb_community_outbox WHERE event_id=? FOR UPDATE", ids[5]).Scan(&lockedID).Error; err != nil || lockedID == 0 {
		t.Fatal("owned row lock failed")
	}
	next, err := repo.Claim(ctx)
	if err != nil || next == nil || next.EventID != ids[6] {
		t.Fatalf("locked head must be skipped for the next due id: event=%+v err=%v", next, err)
	}
	if err = repo.Done(ctx, *next); err != nil {
		t.Fatal("next lease completion failed")
	}
	if err = locked.Rollback().Error; err != nil {
		t.Fatal("owned row lock release failed")
	}
	previouslyLocked, err := repo.Claim(ctx)
	if err != nil || previouslyLocked == nil || previouslyLocked.EventID != ids[5] {
		t.Fatalf("released due id must remain claimable: event=%+v err=%v", previouslyLocked, err)
	}
	if err = repo.Done(ctx, *previouslyLocked); err != nil {
		t.Fatal("released lease completion failed")
	}
	if event, err := repo.Claim(ctx); err != nil || event != nil {
		t.Fatalf("future retry and unexpired lease must stay unavailable: event=%+v err=%v", event, err)
	}
}
