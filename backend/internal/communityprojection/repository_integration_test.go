package communityprojection

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

type projectionFaultKey struct{}

// This suite uses only explicitly configured dependencies and exact random
// fixture IDs. It never truncates a projection, resets offsets or starts Kafka
// consumers. The named lock serializes tests that share the group control rows.
func TestIntegrationProjectionRepository(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated projection dependencies unavailable")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("dependency cleanup failed")
		}
	})
	connection, err := resources.SQL.Conn(ctx)
	if err != nil {
		t.Fatal("cannot reserve projection fixture lock")
	}
	var acquired int
	if connection.QueryRowContext(ctx, "SELECT GET_LOCK('go072_integration',30)").Scan(&acquired) != nil || acquired != 1 {
		_ = connection.Close()
		t.Fatal("another projection test owns the fixture window")
	}
	t.Cleanup(func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released int
		if connection.QueryRowContext(release, "SELECT RELEASE_LOCK('go072_integration')").Scan(&released) != nil || released != 1 {
			t.Error("cannot release projection fixture lock")
		}
		_ = connection.Close()
	})
	repo := New(resources.ORM)
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("cannot allocate projection fixture identity")
	}
	base := uint64(9300000000000000) + binary.BigEndian.Uint64(entropy[:])%1000000000000
	serial := uint64(0)
	next := func() string { serial++; return strconv.FormatUint(base+serial, 10) }
	ownedBodies := [][]byte{}
	ownedNotes := []string{}
	ownedPairs := [][2]string{}
	track := func(body []byte) []byte { ownedBodies = append(ownedBodies, body); return body }
	newPair := func() (string, string) {
		user, target := next(), next()
		ownedPairs = append(ownedPairs, [2]string{user, target})
		return user, target
	}
	exec := func(t *testing.T, query string, args ...any) {
		t.Helper()
		if err := resources.ORM.WithContext(ctx).Exec(query, args...).Error; err != nil {
			t.Fatal("cannot mutate exact owned projection fixture")
		}
	}
	count := func(t *testing.T, query string, want int, args ...any) {
		t.Helper()
		var actual int
		if err := resources.SQL.QueryRowContext(ctx, query, args...).Scan(&actual); err != nil || actual != want {
			t.Fatalf("persistent invariant expected %d, observed %d (%v)", want, actual, err)
		}
	}
	newNote := func(t *testing.T, version int) (string, string) {
		t.Helper()
		id, author := next(), next()
		ownedNotes = append(ownedNotes, id)
		exec(t, `INSERT INTO tb_blog(id,shop_id,user_id,title,images,content,status,publish_version,create_time)
 VALUES(?,0,?,'owned GO072 note','','owned fixture','PUBLISHED',?,'2026-10-28 10:11:12')`, id, author, version)
		return id, author
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, body := range ownedBodies {
			event, err := Decode(body)
			if err == nil {
				if err := resources.ORM.WithContext(cleanup).Exec("DELETE FROM tb_community_projection_event WHERE projection IN (?,?) AND event_id=?", Follow, Feed, event.EventID).Error; err != nil {
					t.Error("cannot remove exact owned receipt")
				}
			}
		}
		for _, pair := range ownedPairs {
			if err := resources.ORM.WithContext(cleanup).Exec("DELETE FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", pair[0], pair[1]).Error; err != nil {
				t.Error("cannot remove exact owned follow projection")
			}
		}
		for _, id := range ownedNotes {
			for _, query := range []string{"DELETE FROM tb_community_note_projection WHERE note_id=?", "DELETE FROM tb_blog WHERE id=?"} {
				if err := resources.ORM.WithContext(cleanup).Exec(query, id).Error; err != nil {
					t.Error("cannot remove exact owned note fixture")
				}
			}
		}
	})
	t.Run("follow_relation_versions_survive_reordering_duplicates_and_author_shared_partition", func(t *testing.T) {
		user, target := newPair()
		for _, version := range []int{3, 1, 2, 3} {
			body := track(followBody(user, target, version, version != 2))
			if err := repo.Apply(ctx, Follow, body); err != nil {
				t.Fatal("valid reordered follow event failed")
			}
		}
		count(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND active=1 AND version=3", 1, user, target)
		count(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id IN (?,?,?)", 3, Follow, "FOLLOW:"+user+":"+target+":1", "FOLLOW:"+user+":"+target+":2", "FOLLOW:"+user+":"+target+":3")
		other := next()
		ownedPairs = append(ownedPairs, [2]string{other, target})
		if err := repo.Apply(ctx, Follow, track(followBody(other, target, 1, false))); err != nil {
			t.Fatal("another user of the same author incorrectly shared its version")
		}
		count(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE target_id=? AND active=1", 1, target)
		count(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND active=0 AND version=1", 1, other, target)
	})
	t.Run("concurrent_first_relation_and_same_receipt_apply_atomically", func(t *testing.T) {
		user, target := newPair()
		const size = 20
		bodies := make([][]byte, size)
		for i := range size {
			bodies[i] = track(followBody(user, target, i+1, (i+1)%2 == 1))
		}
		start := make(chan struct{})
		var wait sync.WaitGroup
		failures := make(chan error, size)
		for _, body := range bodies {
			wait.Add(1)
			go func(body []byte) { defer wait.Done(); <-start; failures <- repo.Apply(ctx, Follow, body) }(body)
		}
		close(start)
		wait.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal("concurrent first relation application failed")
			}
		}
		count(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND active=0 AND version=20", 1, user, target)
		start = make(chan struct{})
		failures = make(chan error, size)
		for range size {
			wait.Add(1)
			go func() { defer wait.Done(); <-start; failures <- repo.Apply(ctx, Follow, bodies[size-1]) }()
		}
		close(start)
		wait.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				var dbError *mysqldriver.MySQLError
				if errors.As(err, &dbError) {
					t.Fatalf("concurrent same receipt was not idempotent (mysql code %d)", dbError.Number)
				}
				t.Fatal("concurrent same receipt was not idempotent")
			}
		}
		count(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?", 1, Follow, "FOLLOW:"+user+":"+target+":20")
	})
	t.Run("same_event_id_conflicting_state_never_overwrites_effect_or_receipt", func(t *testing.T) {
		user, target := newPair()
		body := track(followBody(user, target, 1, true))
		if err := repo.Apply(ctx, Follow, body); err != nil {
			t.Fatal(err)
		}
		if err := repo.Apply(ctx, Follow, track(followBody(user, target, 1, false))); !errors.Is(err, ErrConflict) {
			t.Fatal("same ID/different target state must be a persistent conflict")
		}
		count(t, "SELECT active FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", 1, user, target)
		count(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?", 1, Follow, "FOLLOW:"+user+":"+target+":1")
	})
	t.Run("known_irrelevant_events_have_independent_ignored_receipts_unknowns_have_none", func(t *testing.T) {
		user, target := newPair()
		note, author := newNote(t, 1)
		follow, noteEvent, like := track(followBody(user, target, 1, "true")), track(noteBody(note, author, 1)), track(likeBody(note, user, 1, true))
		for _, body := range [][]byte{noteEvent, like} {
			if err := repo.Apply(ctx, Follow, body); err != nil {
				t.Fatal("known irrelevant event did not receive a valid ignored receipt")
			}
		}
		for _, body := range [][]byte{follow, like} {
			if err := repo.Apply(ctx, Feed, body); err != nil {
				t.Fatal("independent group ignored receipt failed")
			}
		}
		if err := repo.Apply(ctx, Follow, follow); err != nil {
			t.Fatal(err)
		}
		if err := repo.Apply(ctx, Feed, noteEvent); err != nil {
			t.Fatal(err)
		}
		for _, projection := range []string{Follow, Feed} {
			count(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id IN (?,?,?)", 3, projection, "FOLLOW:"+user+":"+target+":1", "NOTE:"+note+":1:PUBLISHED", "LIKE:"+note+":"+user+":1")
		}
		unknown := bytes.Replace(follow, []byte("FOLLOW_CHANGED"), []byte("FUTURE_EVENT"), 1)
		if err := repo.Apply(ctx, Follow, unknown); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown event kind was silently consumed")
		}
		if err := repo.Apply(ctx, "wrong-group", follow); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown projection was accepted")
		}
	})
	t.Run("note_catalog_uses_db_author_and_time_while_versions_never_go_backwards", func(t *testing.T) {
		note, author := newNote(t, 3)
		for _, version := range []int{3, 1, 2, 3} {
			if err := repo.Apply(ctx, Feed, track(noteBody(note, author, version))); err != nil {
				t.Fatal("note reorder/replay failed")
			}
		}
		count(t, "SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id=? AND author_id=? AND version=3 AND published_at='2026-10-28 10:11:12'", 1, note, author)
		var stored struct{ EventHash []byte }
		if resources.ORM.Raw("SELECT event_hash FROM tb_community_note_projection WHERE note_id=?", note).Scan(&stored).Error != nil {
			t.Fatal("cannot inspect note receipt hash")
		}
		event, _ := Decode(noteBody(note, author, 3))
		if !bytes.Equal(stored.EventHash, event.Hash[:]) {
			t.Fatal("note catalog did not preserve the latest applied canonical hash")
		}
	})
	t.Run("missing_unpublished_future_or_foreign_note_fact_rolls_back_receipt", func(t *testing.T) {
		note, author := newNote(t, 1)
		for _, condition := range []string{"missing", "unpublished", "future", "foreign"} {
			version, claimed := 1, author
			if condition == "missing" {
				exec(t, "DELETE FROM tb_blog WHERE id=?", note)
			}
			if condition == "unpublished" {
				exec(t, `INSERT INTO tb_blog(id,shop_id,user_id,title,images,content,status,publish_version,create_time)
 VALUES(?,0,?,'owned','','owned','DRAFT',1,'2026-10-28 10:11:12')`, note, author)
			}
			if condition == "future" {
				exec(t, "UPDATE tb_blog SET status='PUBLISHED' WHERE id=?", note)
				version = 2
			}
			if condition == "foreign" {
				claimed = next()
			}
			body := track(noteBody(note, claimed, version))
			if err := repo.Apply(ctx, Feed, body); !errors.Is(err, ErrFactUnavailable) {
				t.Fatal("unavailable/mismatched note fact must remain retryable")
			}
			count(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?", 0, Feed, "NOTE:"+note+":"+strconv.Itoa(version)+":PUBLISHED")
			count(t, "SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id=?", 0, note)
		}
		if err := repo.Apply(ctx, Feed, track(noteBody(note, author, 1))); err != nil {
			t.Fatal("fact becoming available did not permit safe retry")
		}
	})
	t.Run("projection_or_receipt_failure_rolls_back_both_sides_and_outer_transaction_controls_replay", func(t *testing.T) {
		callback := "go072_inject_owned_projection_failure"
		if err := resources.ORM.Callback().Raw().Before("gorm:raw").Register(callback, func(tx *gorm.DB) {
			fault, _ := tx.Statement.Context.Value(projectionFaultKey{}).(string)
			query := tx.Statement.SQL.String()
			if (fault == "receipt" && strings.Contains(query, "INSERT INTO tb_community_projection_event")) || (fault == "effect" && strings.Contains(query, "UPDATE tb_community_follow_projection SET active=")) {
				tx.AddError(errors.New("injected projection persistence failure"))
			}
		}); err != nil {
			t.Fatal("cannot inject owned projection write failure")
		}
		t.Cleanup(func() { _ = resources.ORM.Callback().Raw().Remove(callback) })
		for _, fault := range []string{"receipt", "effect"} {
			user, target := newPair()
			body := track(followBody(user, target, 1, true))
			faultCtx := context.WithValue(ctx, projectionFaultKey{}, fault)
			if err := repo.Apply(faultCtx, Follow, body); err == nil {
				t.Fatal("persistence failure produced a success")
			}
			count(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", 0, user, target)
			count(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?", 0, Follow, "FOLLOW:"+user+":"+target+":1")
		}
		user, target := newPair()
		body := track(followBody(user, target, 1, true))
		sentinel := errors.New("outer replay resolution rollback")
		err := resources.ORM.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := New(tx).Apply(ctx, Follow, body); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatal("nested repository did not preserve outer replay transaction")
		}
		count(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", 0, user, target)
		count(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?", 0, Follow, "FOLLOW:"+user+":"+target+":1")
	})
	t.Run("bootstrap_same_version_accepts_only_matching_state_author_and_time", func(t *testing.T) {
		var zero [32]byte
		user, target := newPair()
		exec(t, "INSERT INTO tb_community_follow_projection(user_id,target_id,active,version,event_hash,last_event_id) VALUES(?,?,1,4,?,'')", user, target, zero[:])
		if err := repo.Apply(ctx, Follow, track(followBody(user, target, 4, false))); !errors.Is(err, ErrConflict) {
			t.Fatal("bootstrap same-version changed the current target state")
		}
		if err := repo.Apply(ctx, Follow, track(followBody(user, target, 4, true))); err != nil {
			t.Fatal("matching bootstrap follow could not establish its canonical digest")
		}
		count(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND active=1 AND version=4 AND last_event_id<>''", 1, user, target)
		note, author := newNote(t, 2)
		exec(t, "INSERT INTO tb_community_note_projection(note_id,author_id,version,event_hash,last_event_id,published_at) VALUES(?,?,2,?,'','2026-10-28 10:11:11')", note, author, zero[:])
		body := track(noteBody(note, author, 2))
		if err := repo.Apply(ctx, Feed, body); !errors.Is(err, ErrConflict) {
			t.Fatal("bootstrap changed a same-version publish timestamp")
		}
		exec(t, "UPDATE tb_community_note_projection SET published_at='2026-10-28 10:11:12' WHERE note_id=?", note)
		if err := repo.Apply(ctx, Feed, body); err != nil {
			t.Fatal("matching bootstrap note could not establish its canonical digest")
		}
		count(t, "SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id=? AND author_id=? AND version=2 AND last_event_id<>''", 1, note, author)
	})
	t.Run("apply_waits_for_rebuild_control_row_and_does_not_interleave_effects", func(t *testing.T) {
		user, target := newPair()
		body := track(followBody(user, target, 2, false))
		blocker, err := resources.SQL.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("cannot acquire rebuild barrier fixture")
		}
		defer blocker.Rollback()
		var group string
		if blocker.QueryRowContext(ctx, "SELECT projection FROM tb_community_projection_control WHERE projection=? FOR UPDATE", Follow).Scan(&group) != nil {
			t.Fatal("cannot lock current projection control")
		}
		arrived := make(chan struct{}, 1)
		callback := "go072_observe_apply_barrier"
		if err := resources.ORM.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Context.Value(projectionFaultKey{}) == "barrier" && strings.Contains(tx.Statement.SQL.String(), "FROM tb_community_projection_control") {
				select {
				case arrived <- struct{}{}:
				default:
				}
			}
		}); err != nil {
			t.Fatal("cannot observe apply barrier query")
		}
		defer resources.ORM.Callback().Row().Remove(callback)
		waitCtx, cancel := context.WithTimeout(context.WithValue(ctx, projectionFaultKey{}, "barrier"), 5*time.Second)
		defer cancel()
		result, done := make(chan error, 1), make(chan struct{})
		go func() { defer close(done); result <- repo.Apply(waitCtx, Follow, body) }()
		defer func() {
			_ = blocker.Rollback()
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("blocked projection application did not finish")
			}
		}()
		select {
		case <-arrived:
		case <-time.After(3 * time.Second):
			t.Fatal("Apply never reached the current control row")
		}
		select {
		case <-result:
			t.Fatal("Apply bypassed the rebuild barrier")
		case <-time.After(50 * time.Millisecond):
		}
		if err := blocker.Commit(); err != nil {
			t.Fatal("cannot release rebuild barrier")
		}
		select {
		case err := <-result:
			if err != nil {
				t.Fatal("Apply did not recover after rebuild barrier released")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Apply remained blocked after barrier release")
		}
		count(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND version=2 AND active=0", 1, user, target)
	})
}
