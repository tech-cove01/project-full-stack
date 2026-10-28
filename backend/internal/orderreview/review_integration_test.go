package orderreview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func TestIntegrationReviewInterruptedReplay(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("requires isolated MySQL/Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated config")
	}
	r, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	var entropy [10]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	id := "review-" + hex.EncodeToString(entropy[:])
	t.Cleanup(func() { _ = r.ORM.Exec("DELETE FROM tb_order_review WHERE order_id=?", id).Error })
	repo := NewRepository(r.ORM)
	ctx := context.Background()
	if err = repo.Record(ctx, id, "11", "21", "1-0", 5, errors.New("unknown")); err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.Claim(ctx, id); err != nil || !claimed {
		t.Fatal("initial replay not claimed")
	}
	// Simulate process exit after the durable claim but before XADD. The
	// operator must be able to repeat the same message, not create a new order.
	if claimed, err := repo.Claim(ctx, id); err != nil || !claimed {
		t.Fatalf("interrupted REQUEUED record cannot be replayed: claimed=%v err=%v", claimed, err)
	}
	if err := repo.Record(ctx, id, "12", "21", "2-0", 5, errors.New("other owner")); !errors.Is(err, ErrNotDurable) {
		t.Fatal("another owner's review row was treated as durable evidence")
	}
	if err = repo.Resolve(ctx, id); err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.Claim(ctx, id); err != nil || claimed {
		t.Fatal("resolved record must not replay")
	}
}
