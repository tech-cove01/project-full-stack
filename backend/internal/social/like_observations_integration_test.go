package social

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/communitycache"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
)

// This is two finite state changes through the real handler and stores, not a
// capacity run. The deliberate 110ms postcommit barrier tests the slow ring.
func TestIntegrationLikeObservations(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
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
	author, reader := insertUser(t, r, "135"), insertUser(t, r, "136")
	note := insertNote(t, r, author, t.Name(), time.Now().Truncate(time.Second))
	noteID, userID := strconv.FormatUint(note, 10), strconv.FormatUint(reader, 10)
	var phone string
	if err = r.SQL.QueryRowContext(ctx, "SELECT phone FROM tb_user WHERE id=?", reader).Scan(&phone); err != nil {
		t.Fatal("cannot read owned user's phone")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, statement := range []string{
			"DELETE FROM tb_community_outbox WHERE event_id LIKE ?",
			"DELETE FROM tb_blog_like WHERE blog_id=?",
			"DELETE FROM tb_blog WHERE id=?",
			"DELETE FROM tb_user WHERE id IN (?,?)",
		} {
			var args []any
			switch statement {
			case "DELETE FROM tb_community_outbox WHERE event_id LIKE ?":
				args = []any{"LIKE:" + noteID + ":%"}
			case "DELETE FROM tb_user WHERE id IN (?,?)":
				args = []any{author, reader}
			default:
				args = []any{note}
			}
			if err := r.ORM.WithContext(cleanup).Exec(statement, args...).Error; err != nil {
				t.Error("owned observation fixture cleanup failed")
			}
		}
		if err := r.Redis.Del(cleanup, "community:counts:"+noteID, "community:likes:"+noteID,
			"community:likes:versions:"+noteID, "login:code:"+phone, "login:code:throttle:"+phone).Err(); err != nil {
			t.Error("owned observation Redis cleanup failed")
		}
	})
	identity := auth.NewService(r.ORM, r.Redis, "development")
	code, err := identity.SendCode(ctx, phone)
	if err != nil {
		t.Fatal("cannot issue owned login code")
	}
	token, err := identity.Login(ctx, auth.LoginForm{Phone: phone, Code: code.Code})
	if err != nil {
		t.Fatal("cannot log in owned reader")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if identity.Logout(cleanup, token) != nil {
			t.Error("owned observation session logout failed")
		}
		if count, err := r.Redis.Exists(cleanup, "login:token:"+token).Result(); err != nil || count != 0 {
			t.Error("owned observation session remains")
		}
	})
	recorder := taildiag.New()
	taildiag.Instrument(r.ORM, r.SQL, r.Redis)
	counts := communitycache.New(r.Redis, r.ORM)
	service := NewService(r.ORM).WithCounts(counts).WithLikes(communitycache.NewLikes(r.Redis))
	router := httpapi.NewRouter(nil, time.Second, "like-observations-test", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
		Register(routes, service, identity)
	})
	call := func(requestCtx context.Context, liked bool) LikeState {
		t.Helper()
		body := `{"liked":` + strconv.FormatBool(liked) + `}`
		request := httptest.NewRequest(http.MethodPut, "/notes/"+noteID+"/like", strings.NewReader(body)).WithContext(requestCtx)
		request.Header.Set("authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		var reply struct {
			Success bool      `json:"success"`
			Data    LikeState `json:"data"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &reply) != nil || !reply.Success {
			t.Fatal("owned Like handler call failed")
		}
		return reply.Data
	}
	if state := call(ctx, true); !state.Liked || state.Version != 1 || state.Likes != 1 || recorder.Snapshot().Requests != 0 {
		t.Fatal("disabled observation changed Like behavior or recorded work")
	}
	if cached, err := counts.Get(ctx, note); err != nil || cached.Likes != 1 {
		t.Fatal("cannot seed owned counter before cancellation")
	}
	service.WithLikeDiagnostics(recorder)
	marker := "bounded-observation-barrier"
	requestCtx, stopRequest := context.WithCancel(context.WithValue(ctx, likeCancellationKey{}, marker))
	defer stopRequest()
	hook := &likeCancellationHook{token: marker, key: "community:counts:" + noteID}
	hook.beforeDelete = func(derived context.Context) error {
		var active, version int
		var count int64
		if err := r.SQL.QueryRowContext(ctx, `SELECT l.active,l.version,b.liked FROM tb_blog_like l
			JOIN tb_blog b ON b.id=l.blog_id WHERE l.blog_id=? AND l.user_id=?`, note, reader).Scan(&active, &version, &count); err != nil || active != 0 || version != 2 || count != 0 {
			t.Error("postcommit observation barrier did not see committed facts")
		}
		stopRequest()
		deadline, ok := derived.Deadline()
		if derived.Err() != nil || !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 2*time.Second {
			t.Error("observation changed independent postcommit cancellation/deadline")
		}
		timer := time.NewTimer(110 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-derived.Done():
			return derived.Err()
		}
	}
	r.Redis.AddHook(hook)
	if state := call(requestCtx, false); state.Liked || state.Version != 2 || state.Likes != 0 || !hook.used || !hook.markSeen || !errors.Is(requestCtx.Err(), context.Canceled) {
		t.Fatal("observed committed unlike did not converge after caller cancellation")
	}
	out := recorder.Snapshot()
	if out.Requests != 1 || len(out.SlowTraces) != 1 || out.SlowTraces[0].Scope != "like.http" || !out.SlowTraces[0].Canceled || out.SlowTraces[0].Error || out.SlowTraces[0].Truncated {
		t.Fatal("expected one bounded canceled HTTP trace with successful postcommit completion")
	}
	wanted := map[string]bool{"auth.resolve": true, "like.http": true, "like.service": true,
		"like.transaction_total": true, "sql.commit_call": true, "like.postcommit_invalidate": true,
		"like.postcommit_state": true, "like.postcommit_mark": true}
	for _, stage := range out.Stages {
		if stage.Active != 0 || wanted[stage.Name] && (stage.Count != 1 || stage.Errors != 0) {
			t.Errorf("unexpected completed stage %s: count=%d active=%d errors=%d", stage.Name, stage.Count, stage.Active, stage.Errors)
		}
		delete(wanted, stage.Name)
	}
	if len(wanted) != 0 {
		t.Fatal("required Like stages are missing from the snapshot")
	}
	encoded, err := json.Marshal(out)
	if err != nil || strings.Contains(string(encoded), token) || strings.Contains(string(encoded), phone) || strings.Contains(string(encoded), marker) {
		t.Error("observations exported private context/session data")
	}
	var events int
	if err := r.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM tb_community_outbox WHERE event_id LIKE ?", "LIKE:"+noteID+":%").Scan(&events); err != nil || events != 2 {
		t.Error("finite observed changes lost their authoritative events")
	}
	if member, err := r.Redis.SIsMember(ctx, "community:likes:"+noteID, userID).Result(); err != nil || member {
		t.Error("observed unlike left stale membership")
	}
	if version, err := r.Redis.HGet(ctx, "community:likes:versions:"+noteID, userID).Result(); err != nil || version != "2" {
		t.Error("observed unlike lost the projection version")
	}
	if keys, err := r.Redis.Exists(ctx, "community:counts:"+noteID).Result(); err != nil || keys != 0 {
		t.Error("observed unlike retained the stale Redis count")
	}
	if current, err := counts.Get(ctx, note); err != nil || current.Likes != 0 {
		t.Error("observed unlike retained the stale local count")
	}
}
