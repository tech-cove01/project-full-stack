package social

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/communitycache"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

type likeCancellationKey struct{}

// The hook only intercepts this test's first postcommit DEL. It neither mocks
// Redis nor races a timer against Commit: the independent SQL read proves that
// the write is visible before cancellation is delivered.
type likeCancellationHook struct {
	token        string
	key          string
	beforeDelete func(context.Context) error
	used         bool
	markSeen     bool
}

func (hook *likeCancellationHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (hook *likeCancellationHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (hook *likeCancellationHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if ctx.Value(likeCancellationKey{}) != hook.token {
			return next(ctx, cmd)
		}
		args := cmd.Args()
		if cmd.Name() == "del" && len(args) == 2 && args[1] == hook.key && !hook.used {
			hook.used = true
			if err := hook.beforeDelete(ctx); err != nil {
				return err
			}
		}
		if cmd.Name() == "evalsha" || cmd.Name() == "eval" {
			hook.markSeen = true
		}
		return next(ctx, cmd)
	}
}

func TestIntegrationLikeCommitCancellation(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	author, reader := insertUser(t, r, "135"), insertUser(t, r, "136")
	userID := strconv.FormatUint(reader, 10)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_user WHERE id IN (?,?)", author, reader).Error
	})
	newNote := func(t *testing.T) (uint64, string) {
		t.Helper()
		note := insertNote(t, r, author, t.Name(), time.Now().Truncate(time.Second))
		noteID := strconv.FormatUint(note, 10)
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_community_outbox WHERE event_id LIKE ?", "LIKE:"+noteID+":%").Error
			_ = r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_blog_like WHERE blog_id=?", note).Error
			_ = r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_blog WHERE id=?", note).Error
			_ = r.Redis.Del(cleanup, "community:counts:"+noteID, "community:likes:"+noteID, "community:likes:versions:"+noteID).Err()
		})
		return note, noteID
	}
	assertFacts := func(t *testing.T, note uint64, active, version int, count, events int64) {
		t.Helper()
		var gotActive, gotVersion, rows int
		var gotCount, gotEvents int64
		err := r.SQL.QueryRowContext(ctx, `SELECT b.liked,COALESCE(l.active,0),COALESCE(l.version,0),
			(SELECT COUNT(*) FROM tb_blog_like WHERE blog_id=? AND user_id=?),
			(SELECT COUNT(*) FROM tb_community_outbox WHERE event_id LIKE ?)
			FROM tb_blog b LEFT JOIN tb_blog_like l ON l.blog_id=b.id AND l.user_id=? WHERE b.id=?`,
			note, reader, "LIKE:"+strconv.FormatUint(note, 10)+":%", reader, note).
			Scan(&gotCount, &gotActive, &gotVersion, &rows, &gotEvents)
		wantRows := 1
		if version == 0 {
			wantRows = 0
		}
		if err != nil || gotActive != active || gotVersion != version || gotCount != count || gotEvents != events || rows != wantRows {
			t.Errorf("committed facts: active=%d version=%d count=%d events=%d rows=%d err=%v; want %d/%d/%d/%d/%d",
				gotActive, gotVersion, gotCount, gotEvents, rows, err, active, version, count, events, wantRows)
		}
	}
	assertProjection := func(t *testing.T, noteID, version string, active bool) {
		t.Helper()
		gotActive, err := r.Redis.SIsMember(ctx, "community:likes:"+noteID, userID).Result()
		if err != nil || gotActive != active {
			t.Errorf("projection membership=%v err=%v, want %v", gotActive, err, active)
		}
		gotVersion, err := r.Redis.HGet(ctx, "community:likes:versions:"+noteID, userID).Result()
		if err != nil || gotVersion != version {
			t.Errorf("projection version=%q err=%v, want %q", gotVersion, err, version)
		}
	}

	t.Run("cancel_before_commit_rolls_back", func(t *testing.T) {
		note, noteID := newNote(t)
		token := "before:" + noteID
		requestCtx, stop := context.WithCancel(context.WithValue(ctx, likeCancellationKey{}, token))
		defer stop()
		reached := false
		callback := "cap09_like_cancel_before_commit_" + noteID
		if err := r.ORM.Callback().Raw().After("gorm:raw").Register(callback, func(tx *gorm.DB) {
			if tx.Error == nil && tx.Statement.Context.Value(likeCancellationKey{}) == token &&
				strings.Contains(tx.Statement.SQL.String(), "INSERT IGNORE INTO tb_community_outbox") {
				reached = true
				stop()
			}
		}); err != nil {
			t.Fatal("cannot register precommit cancellation barrier")
		}
		defer r.ORM.Callback().Raw().Remove(callback)
		service := NewService(r.ORM).WithLikes(communitycache.NewLikes(r.Redis))
		// database/sql may finish its automatic rollback before Commit observes
		// cancellation; both terminal errors must leave exactly the same facts.
		if _, err := service.SetLike(requestCtx, userID, noteID, true); !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("cancellation before Commit must reject the transaction: %v", err)
		}
		if !reached {
			t.Fatal("transaction never reached its precommit outbox barrier")
		}
		assertFacts(t, note, 0, 0, 0, 0)
		if keys, err := r.Redis.Exists(ctx, "community:likes:"+noteID, "community:likes:versions:"+noteID).Result(); err != nil || keys != 0 {
			t.Errorf("rolled-back write created a projection: keys=%d err=%v", keys, err)
		}
	})

	t.Run("postcommit_cancel_finishes_derived_updates", func(t *testing.T) {
		note, noteID := newNote(t)
		counts := communitycache.New(r.Redis, r.ORM)
		service := NewService(r.ORM).WithCounts(counts).WithLikes(communitycache.NewLikes(r.Redis))
		if state, err := service.SetLike(ctx, userID, noteID, true); err != nil || state.Version != 1 || !state.Liked {
			t.Fatalf("cannot seed committed like: %+v %v", state, err)
		}
		if cached, err := counts.Get(ctx, note); err != nil || cached.Likes != 1 {
			t.Fatalf("cannot seed stale counter cache: %+v %v", cached, err)
		}
		assertProjection(t, noteID, "1", true)
		token := "after:" + noteID
		requestCtx, stop := context.WithCancel(context.WithValue(ctx, likeCancellationKey{}, token))
		defer stop()
		var committed bool
		var derivedErr error
		var deadline time.Time
		var hasDeadline bool
		var remaining time.Duration
		hook := &likeCancellationHook{token: token, key: "community:counts:" + noteID}
		hook.beforeDelete = func(derivedCtx context.Context) error {
			var active, version int
			var count int64
			err := r.SQL.QueryRowContext(ctx, `SELECT l.active,l.version,b.liked FROM tb_blog_like l
				JOIN tb_blog b ON b.id=l.blog_id WHERE l.blog_id=? AND l.user_id=?`, note, reader).
				Scan(&active, &version, &count)
			committed = err == nil && active == 0 && version == 2 && count == 0
			stop()
			derivedErr = derivedCtx.Err()
			deadline, hasDeadline = derivedCtx.Deadline()
			remaining = time.Until(deadline)
			return err
		}
		r.Redis.AddHook(hook)
		state, writeErr := service.SetLike(requestCtx, userID, noteID, false)
		if !hook.used || !committed || !errors.Is(requestCtx.Err(), context.Canceled) {
			t.Fatalf("cancellation was not delivered after a proven commit: hook=%v committed=%v caller=%v", hook.used, committed, requestCtx.Err())
		}
		if derivedErr != nil || !hasDeadline || remaining <= 0 || remaining > 2*time.Second {
			t.Errorf("derived context must survive caller cancellation with a fresh <=2s deadline: err=%v deadline=%v remaining=%s", derivedErr, hasDeadline, remaining)
		}
		if writeErr != nil || state.Liked || state.Version != 2 || state.Likes != 0 {
			t.Errorf("committed unlike must finish after caller cancellation: %+v %v", state, writeErr)
		}
		assertFacts(t, note, 0, 2, 0, 2)
		assertProjection(t, noteID, "2", false)
		if !hook.markSeen {
			t.Error("projection Mark lost the original context values")
		}
		if keys, err := r.Redis.Exists(ctx, "community:counts:"+noteID).Result(); err != nil || keys != 0 {
			t.Errorf("postcommit invalidation left the stale Redis counter: keys=%d err=%v", keys, err)
		}
		if current, err := counts.Get(ctx, note); err != nil || current.Likes != 0 {
			t.Errorf("postcommit invalidation left the stale local counter: %+v %v", current, err)
		}
	})

	t.Run("older_projection_version_cannot_restore_like", func(t *testing.T) {
		note, noteID := newNote(t)
		likes := communitycache.NewLikes(r.Redis)
		service := NewService(r.ORM).WithLikes(likes)
		if _, err := service.SetLike(ctx, userID, noteID, true); err != nil {
			t.Fatal(err)
		}
		if state, err := service.SetLike(ctx, userID, noteID, false); err != nil || state.Version != 2 || state.Liked {
			t.Fatalf("cannot seed later unlike: %+v %v", state, err)
		}
		if changed, err := likes.Mark(ctx, note, reader, true, 1); err != nil || changed {
			t.Errorf("older like bypassed the version fence: changed=%v err=%v", changed, err)
		}
		assertFacts(t, note, 0, 2, 0, 2)
		assertProjection(t, noteID, "2", false)
	})
}
