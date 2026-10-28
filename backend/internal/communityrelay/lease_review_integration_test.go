package communityrelay

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func TestIntegrationExhaustedLeaseDoesNotBlockQueue(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	prefix := "REVIEW:" + randomSuffix(t)
	t.Cleanup(func() {
		_ = r.ORM.Exec("DELETE FROM tb_community_outbox WHERE event_id IN (?,?)", prefix+":1", prefix+":2").Error
	})
	for _, suffix := range []string{":1", ":2"} {
		if err = r.ORM.Exec(`INSERT INTO tb_community_outbox(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload) VALUES(?,'FOLLOW','42',1,'FOLLOW_CHANGED','{}')`, prefix+suffix).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err = r.ORM.Exec("UPDATE tb_community_outbox SET delivery_status='PROCESSING',attempts=8,lease_until=DATE_SUB(NOW(3),INTERVAL 1 SECOND) WHERE event_id=?", prefix+":1").Error; err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(r.ORM)
	for i := 0; i < 3; i++ {
		event, err := repo.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event != nil {
			if event.EventID != prefix+":2" {
				t.Fatalf("unexpected event %s", event.EventID)
			}
			if deliveryState(t, r, prefix+":1") != "NEEDS_REVIEW" {
				t.Fatal("exhausted lease must remain durably visible for review")
			}
			return
		}
	}
	t.Fatal("expired eighth-attempt event permanently blocks later due event")
}
