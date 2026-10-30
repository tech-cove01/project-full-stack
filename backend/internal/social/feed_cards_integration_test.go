package social

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/communitycache"
	"github.com/cityloop/fullstack-go/internal/communitycards"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

type cardFeedFixture struct {
	r            *database.Resources
	service      *Service
	cards        *communitycards.Cache
	identity     *auth.Service
	server       *httptest.Server
	users, notes []uint64
	tokens       []string
	ctx          context.Context
}

func newCardFeed(t *testing.T) *cardFeedFixture {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	r, err := database.Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal("isolated dependencies unavailable")
	}
	f := &cardFeedFixture{r: r, ctx: ctx}
	t.Cleanup(func() {
		if f.server != nil {
			f.server.Close()
		}
		if f.cards != nil {
			if f.cards.Close() != nil {
				t.Error("owned card close failed")
			}
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		defer cancel()
		defer func() {
			if r.Close() != nil {
				t.Error("dependency cleanup failed")
			}
		}()
		for _, token := range f.tokens {
			if f.identity.Logout(cleanup, token) != nil {
				t.Error("owned logout failed")
			}
		}
		for _, id := range f.notes {
			for version := uint64(0); version < 4; version++ {
				if r.Redis.Del(cleanup, "community:note-card:v1:"+strconv.FormatUint(id, 10)+":"+strconv.FormatUint(version, 10)).Err() != nil {
					t.Error("owned card key cleanup failed")
				}
			}
			if r.Redis.Del(cleanup, "community:counts:"+strconv.FormatUint(id, 10)).Err() != nil {
				t.Error("owned count key cleanup failed")
			}
			for _, statement := range []string{"DELETE FROM tb_blog_like WHERE blog_id=?", "DELETE FROM tb_community_note_projection WHERE note_id=?", "DELETE FROM tb_community_outbox WHERE aggregate_type='BLOG' AND aggregate_id=?", "DELETE FROM tb_blog WHERE id=?"} {
				if r.ORM.WithContext(cleanup).Exec(statement, id).Error != nil {
					t.Error("owned note cleanup failed")
				}
			}
		}
		for _, id := range f.users {
			var phone string
			if r.ORM.WithContext(cleanup).Raw("SELECT phone FROM tb_user WHERE id=?", id).Scan(&phone).Error != nil || phone == "" {
				t.Error("owned phone cleanup lookup failed")
			} else if r.Redis.Del(cleanup, "login:code:"+phone, "login:code:throttle:"+phone).Err() != nil {
				t.Error("owned code cleanup failed")
			}
			for _, statement := range []string{"DELETE FROM tb_community_outbox WHERE aggregate_type='FOLLOW' AND aggregate_id=?", "DELETE FROM tb_follow WHERE user_id=?", "DELETE FROM tb_user WHERE id=?"} {
				if r.ORM.WithContext(cleanup).Exec(statement, id).Error != nil {
					t.Error("owned user cleanup failed")
				}
			}
		}
	})
	for _, prefix := range []string{"138", "137", "136"} {
		f.users = append(f.users, insertUser(t, r, prefix))
	}
	f.identity = auth.NewService(r.ORM, r.Redis, "development")
	for _, id := range f.users[:2] {
		var phone string
		if r.ORM.Raw("SELECT phone FROM tb_user WHERE id=?", id).Scan(&phone).Error != nil {
			t.Fatal("phone lookup failed")
		}
		code, e := f.identity.SendCode(ctx, phone)
		if e != nil {
			t.Fatal("owned code failed")
		}
		token, e := f.identity.Login(ctx, auth.LoginForm{Phone: phone, Code: code.Code})
		if e != nil {
			t.Fatal("owned login failed")
		}
		f.tokens = append(f.tokens, token)
	}
	at := time.Now().Truncate(time.Second)
	for _, title := range []string{"缓存甲", "缓存乙"} {
		f.notes = append(f.notes, insertNote(t, r, f.users[2], title, at))
	}
	for _, id := range f.notes {
		if r.ORM.Exec("UPDATE tb_blog SET images=' a.png, ,b.png ',content=? WHERE id=?", "公共正文完整", id).Error != nil {
			t.Fatal("owned content setup failed")
		}
	}
	f.cards = communitycards.New(ctx, r.Redis, r.ORM)
	f.service = NewService(r.ORM).WithProjections().WithCounts(communitycache.New(r.Redis, r.ORM)).WithCards(f.cards)
	for _, id := range f.users[:2] {
		if _, e := f.service.SetFollow(ctx, strconv.FormatUint(id, 10), strconv.FormatUint(f.users[2], 10), true); e != nil {
			t.Fatal("owned follow failed")
		}
	}
	return f
}

func (f *cardFeedFixture) start(t *testing.T) {
	t.Helper()
	router := gin.New()
	Register(router, f.service, f.identity)
	f.server = httptest.NewServer(router)
}

func (f *cardFeedFixture) request(t *testing.T, user int, query string) (FeedPage, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, f.server.URL+"/feed/notes?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", f.tokens[user])
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal("real Feed HTTP failed")
	}
	defer response.Body.Close()
	var body struct {
		Success bool     `json:"success"`
		Data    FeedPage `json:"data"`
	}
	if json.NewDecoder(response.Body).Decode(&body) != nil {
		t.Fatal("invalid Feed HTTP JSON")
	}
	if response.StatusCode == 200 && !body.Success {
		t.Fatal("HTTP200 without successful Feed")
	}
	return body.Data, response.StatusCode
}

func TestIntegrationFeedCardsHTTPColdHotAndFacts(t *testing.T) {
	f := newCardFeed(t)
	if _, err := f.service.SetLike(f.ctx, strconv.FormatUint(f.users[0], 10), strconv.FormatUint(f.notes[1], 10), true); err != nil {
		t.Fatal("owned like failed")
	}
	baseline, err := NewService(f.r.ORM).WithProjections().Feed(f.ctx, strconv.FormatUint(f.users[0], 10), "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	// The only connection must be released by the directory before cold cards/counts.
	f.r.SQL.SetMaxOpenConns(1)
	f.r.SQL.SetMaxIdleConns(1)
	waits := f.r.SQL.Stats().WaitCount
	f.start(t)
	before := f.cards.Stats()
	cold, status := f.request(t, 0, "limit=2")
	if status != 200 || !reflect.DeepEqual(cold, baseline) {
		t.Fatalf("cold HTTP differs from full facts: status=%d page=%+v baseline=%+v", status, cold, baseline)
	}
	after := f.cards.Stats()
	if after.ContentSQLBatches-before.ContentSQLBatches != 1 || after.ContentSQLRows-before.ContentSQLRows != 2 {
		t.Fatal("cold HTTP did not use one batch content SQL")
	}
	hot, status := f.request(t, 1, "limit=2")
	if status != 200 || len(hot.Items) != 2 || hot.Items[0].LikedByMe || !cold.Items[0].LikedByMe || hot.Items[0].Content != cold.Items[0].Content {
		t.Fatal("public cache polluted caller likes")
	}
	if f.cards.Stats().ContentSQLBatches != after.ContentSQLBatches {
		t.Fatal("hot HTTP loaded content SQL")
	}
	t.Logf("actual HTTP cold content SQL batches=%d rows=%d; hot additional batches=%d; current facts and caller likes checked", after.ContentSQLBatches-before.ContentSQLBatches, after.ContentSQLRows-before.ContentSQLRows, f.cards.Stats().ContentSQLBatches-after.ContentSQLBatches)
	if f.r.SQL.Stats().InUse != 0 || f.r.SQL.Stats().WaitCount != waits {
		t.Fatal("cold cards nested or retained snapshot connection")
	}
	first, status := f.request(t, 0, "limit=1")
	if status != 200 || first.NextCursor == nil {
		t.Fatal("missing cursor")
	}
	q := url.Values{"limit": {"1"}, "cursorTime": {first.NextCursor.PublishedAt}, "cursorId": {first.NextCursor.ID}}
	next, status := f.request(t, 0, q.Encode())
	if status != 200 || len(next.Items) != 1 || next.Items[0].ID == first.Items[0].ID {
		t.Fatal("same-second cached cursor repeated or skipped")
	}
	if _, err = f.service.SetFollow(f.ctx, strconv.FormatUint(f.users[1], 10), strconv.FormatUint(f.users[2], 10), false); err != nil {
		t.Fatal(err)
	}
	empty, status := f.request(t, 1, "limit=2")
	if status != 200 || len(empty.Items) != 0 {
		t.Fatal("cache restored unfollowed content")
	}
	if f.r.ORM.Exec("UPDATE tb_blog SET status='DRAFT' WHERE id=?", f.notes[1]).Error != nil {
		t.Fatal("owned visibility change failed")
	}
	visible, status := f.request(t, 0, "limit=2")
	if status != 200 || len(visible.Items) != 1 || visible.Items[0].ID != strconv.FormatUint(f.notes[0], 10) {
		t.Fatal("warm public fragment bypassed current visibility")
	}
	if f.r.ORM.Exec("UPDATE tb_blog SET publish_version=2,title='新版本',content='新正文' WHERE id=?", f.notes[0]).Error != nil {
		t.Fatal("owned version change failed")
	}
	versioned, status := f.request(t, 0, "limit=2")
	if status != 200 || len(versioned.Items) != 1 || versioned.Items[0].Title != "新版本" || versioned.Items[0].Content != "新正文" {
		t.Fatal("new directory reused old version")
	}
}

func TestIntegrationFeedCardsHTTPVersionRaceAndFailure(t *testing.T) {
	for _, change := range []string{"version", "draft", "database"} {
		t.Run(change, func(t *testing.T) {
			f := newCardFeed(t)
			db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: f.r.SQL, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			if db.Callback().Row().Before("gorm:row").Register("cap07_owned_content_race", func(tx *gorm.DB) {
				query := tx.Statement.SQL.String()
				if !strings.Contains(query, "SELECT id,publish_version,title,content,images FROM tb_blog") {
					return
				}
				if calls.Add(1) != 1 {
					return
				}
				if change == "database" {
					tx.Statement.SQL.Reset()
					tx.Statement.SQL.WriteString("SELECT id FROM cap07_absent_owned_table")
					return
				}
				statement := "UPDATE tb_blog SET publish_version=2,title='竞态新标题',content='竞态新正文' WHERE id=?"
				if change == "draft" {
					statement = "UPDATE tb_blog SET status='DRAFT' WHERE id=?"
				}
				if f.r.ORM.WithContext(f.ctx).Exec(statement, f.notes[1]).Error != nil {
					tx.AddError(gorm.ErrInvalidData)
				}
			}) != nil {
				t.Fatal("controlled content callback failed")
			}
			_ = f.cards.Close()
			f.cards = communitycards.New(f.ctx, f.r.Redis, db)
			f.service.WithCards(f.cards)
			f.start(t)
			page, status := f.request(t, 0, "limit=2")
			if calls.Load() != 1 {
				t.Fatal("content race not entered exactly once")
			}
			if change == "database" {
				if status != 503 || f.service.CardFallbacks() != 0 || f.cards.Stats().L1Entries != 0 {
					t.Fatal("DB failure was masked or cached")
				}
				return
			}
			if status != 200 || f.service.CardFallbacks() != 1 || f.cards.Stats().ContentSQLBatches != 1 {
				t.Fatal("version race did not use exactly one full-fact retry")
			}
			t.Logf("controlled %s change after directory: content SQL attempts=%d full-fact retries=%d HTTP=%d", change, f.cards.Stats().ContentSQLBatches, f.service.CardFallbacks(), status)
			if change == "version" && (len(page.Items) != 2 || page.Items[0].Title != "竞态新标题" || page.Items[0].Content != "竞态新正文" || page.NextCursor == nil) {
				t.Fatal("race mixed old directory/new body")
			}
			if change == "draft" && (len(page.Items) != 1 || page.Items[0].ID != strconv.FormatUint(f.notes[0], 10)) {
				t.Fatal("unpublished race returned partial stale page")
			}
		})
	}
}
