package communityrelay

import (
	"errors"
	"fmt"
	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"reflect"
	"strings"
	"testing"
)

// The test-only callback alters only this fixture's second group and is removed
// before inspection. It proves actual earlier park/lease writes roll back.
func TestIntegrationClaimPointSecondGroupRollback(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprintf("mismatchedFence_%t", mismatch), func(t *testing.T) {
			f := newBatchFixture(t)
			events := f.seed(t, 4)
			db := f.resources.ORM
			if err := db.Exec("UPDATE tb_community_outbox SET attempts=8 WHERE id IN ?", []uint64{events[0].ID, events[1].ID}).Error; err != nil {
				t.Fatal(err)
			}
			before := f.states(t)
			name := "cap10-point-second-group-fault"
			fired := false
			if err := db.Callback().Raw().Before("gorm:raw").Register(name, func(tx *gorm.DB) {
				if fired || !strings.Contains(tx.Statement.SQL.String(), "SET o.delivery_status='PROCESSING'") {
					return
				}
				fired = true
				if mismatch {
					// The selected source fence is0. Change only the second pair's
					// expected attempt, so the first claimed row really changes but
					// the resulting group RowsAffected is short by one.
					tx.Statement.Vars[3] = 7
				} else {
					tx.Statement.SQL.Reset()
					tx.Statement.SQL.WriteString("UPDATE tb_community_outbox SET cap10_missing_column=? WHERE id=?")
					tx.Statement.Vars = []any{1, events[2].ID}
				}
			}); err != nil {
				t.Fatal(err)
			}
			got, err := NewRepository(db).ClaimBatch(f.ctx, 4)
			if removeErr := db.Callback().Raw().Remove(name); removeErr != nil {
				t.Fatal(removeErr)
			}
			if !fired || got != nil || err == nil || mismatch && !errors.Is(err, ErrLeaseLost) {
				t.Fatal("second group did not fail conservatively")
			}
			if !mismatch {
				var actual *mysqldriver.MySQLError
				if !errors.As(err, &actual) || actual.Number != 1054 {
					t.Fatal("expected actual server unknown-column error after first group")
				}
			}
			if !reflect.DeepEqual(before, f.states(t)) {
				t.Fatal("actual second-group failure committed earlier parking or lease prefix")
			}
			var changedErrors int64
			if err := db.Raw("SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN ? AND last_error IS NOT NULL", f.ids).Scan(&changedErrors).Error; err != nil || changedErrors != 0 {
				t.Fatal("parking error text escaped rollback")
			}
		})
	}
}

func TestIntegrationClaimPointMaxAndByteSuffix(t *testing.T) {
	t.Run("max64", func(t *testing.T) {
		f := newBatchFixture(t)
		f.seed(t, 66)
		got, err := NewRepository(f.resources.ORM).ClaimBatch(f.ctx, 64)
		if err != nil || len(got) != 64 {
			t.Fatal("64-row candidate did not retain the count bound")
		}
		for i, state := range f.states(t) {
			if i < 64 {
				if state.Status != "PROCESSING" || state.Attempts != 1 || !state.Leased {
					t.Fatal("64-row lease state differs")
				}
			} else if state.Status != "PENDING" || state.Attempts != 0 || state.Leased {
				t.Fatal("count-limit suffix was changed")
			}
		}
	})
	t.Run("exhausted_after_byte_cutoff", func(t *testing.T) {
		f := newBatchFixture(t)
		seed := f.seed(t, 5)
		if err := f.resources.ORM.Exec("UPDATE tb_community_outbox SET attempts=8 WHERE id IN ?", []uint64{seed[0].ID, seed[2].ID, seed[4].ID}).Error; err != nil {
			t.Fatal(err)
		}
		rows := boundedFixtureEvents(t, f)
		got, err := NewRepository(f.resources.ORM).ClaimBatchBounded(f.ctx, 5, int(eventEncodedUpperBound(rows[1])))
		if err != nil || len(got) != 1 || got[0].ID != rows[1].ID {
			t.Fatal("mixed exhausted byte prefix differs")
		}
		states := f.states(t)
		for _, i := range []int{0, 2} {
			if states[i].Status != "NEEDS_REVIEW" || states[i].Attempts != 8 || states[i].Leased {
				t.Fatal("selected exhausted prefix was not parked")
			}
		}
		if states[3].Status != "PENDING" || states[3].Attempts != 0 || states[3].Leased || states[4].Status != "PENDING" || states[4].Attempts != 8 || states[4].Leased {
			t.Fatal("unselected exhausted suffix was parked or spent an attempt")
		}
	})
}
