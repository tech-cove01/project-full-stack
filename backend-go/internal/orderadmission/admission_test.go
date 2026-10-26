package orderadmission

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInvalidIdentityAndWindowFailBeforeRedis(t *testing.T) {
	admission := New(nil)
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, businessZone)
	for _, id := range []string{"", "0", "01", "+1", "-1", "1e2", "18446744073709551616"} {
		if _, err := admission.Recover(ctx, id, "1"); !errors.Is(err, ErrUnsafe) {
			t.Fatal("invalid voucher ID reached Redis")
		}
		if _, err := admission.Recover(ctx, "1", id); !errors.Is(err, ErrUnsafe) {
			t.Fatal("invalid user ID reached Redis")
		}
		if _, err := admission.Reserve(ctx, id, "1", "1", now, now.Add(-time.Hour), now.Add(time.Hour), 1); !errors.Is(err, ErrUnsafe) {
			t.Fatal("invalid reservation ID reached Redis")
		}
	}
	for _, id := range []string{"0", "01", "-1", "9223372036854775808"} {
		if _, err := admission.Reserve(ctx, "1", "1", id, now, now.Add(-time.Hour), now.Add(time.Hour), 1); !errors.Is(err, ErrUnsafe) {
			t.Fatal("order ID must fit the signed database key")
		}
	}
	for _, version := range []int{0, -1, 2147483648} {
		if _, err := admission.Reserve(ctx, "1", "1", "1", now, now.Add(-time.Hour), now.Add(time.Hour), version); !errors.Is(err, ErrUnsafe) {
			t.Fatal("unsafe version reached Redis")
		}
	}
	if _, err := admission.Reserve(ctx, "1", "1", "1", now, now, now, 1); !errors.Is(err, ErrUnsafe) {
		t.Fatal("empty sales window reached Redis")
	}
}

func TestIDTimestampBoundsFailBeforeRedis(t *testing.T) {
	admission := New(nil)
	for _, now := range []time.Time{
		time.Date(2021, 12, 31, 23, 59, 59, 0, businessZone),
		time.Date(2100, 1, 1, 0, 0, 0, 0, businessZone),
	} {
		if _, err := admission.NextID(context.Background(), now); !errors.Is(err, ErrUnsafe) {
			t.Fatal("unsupported high bits must fail before touching a counter")
		}
	}
}

func TestAdmissionDecodesExactStringIDs(t *testing.T) {
	code, id, err := decode([]any{int64(8), "9223372036854775807"})
	if err != nil || code != 8 || id != "9223372036854775807" {
		t.Fatal("accepted IDs must remain exact bulk strings")
	}
	for _, response := range [][]any{{int64(0)}, {int64(0), int64(9007199254740993)}, {float64(0), "1"}} {
		if _, _, err := decode(response); !errors.Is(err, ErrUnsafe) {
			t.Fatal("unexpected script response was accepted")
		}
	}
}
