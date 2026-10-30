package social

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/communitycards"
)

func TestIntegrationFeedCardsHTTPSameBatchBurstAndFirstCancellation(t *testing.T) {
	f := newCardFeed(t)
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: f.r.SQL, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	if db.Callback().Row().Before("gorm:row").Register("cap07_owned_block_content", func(tx *gorm.DB) {
		if !strings.Contains(tx.Statement.SQL.String(), "SELECT id,publish_version,title,content,images FROM tb_blog") {
			return
		}
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-tx.Statement.Context.Done():
			}
		})
	}) != nil {
		t.Fatal("content barrier registration failed")
	}
	if f.cards.Close() != nil {
		t.Fatal("old card cache close failed")
	}
	f.cards = communitycards.New(f.ctx, f.r.Redis, db)
	f.service.WithCards(f.cards)
	f.start(t)
	type reply struct {
		page   FeedPage
		status int
		err    error
	}
	request := func(ctx context.Context, user int) reply {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/feed/notes?limit=2", nil)
		if e != nil {
			return reply{err: e}
		}
		req.Header.Set("Authorization", f.tokens[user])
		res, e := f.server.Client().Do(req)
		if e != nil {
			return reply{err: e}
		}
		defer res.Body.Close()
		var body struct {
			Success bool     `json:"success"`
			Data    FeedPage `json:"data"`
		}
		e = json.NewDecoder(res.Body).Decode(&body)
		if e == nil && res.StatusCode == http.StatusOK && !body.Success {
			e = errors.New("Feed HTTP success envelope missing")
		}
		return reply{body.Data, res.StatusCode, e}
	}
	leaderCtx, cancelLeader := context.WithCancel(f.ctx)
	defer cancelLeader()
	leader := make(chan reply, 1)
	go func() { leader <- request(leaderCtx, 0) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual HTTP did not reach cold content SQL")
	}
	const followers = 7
	results := make(chan reply, followers)
	for i := 0; i < followers; i++ {
		go func(user int) { results <- request(f.ctx, user) }(i % 2)
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for f.cards.Stats().FlightJoins < followers {
		if time.Now().After(deadline) {
			t.Fatal("HTTP followers did not join the confirmed blocked batch")
		}
		runtime.Gosched()
	}
	cancelLeader()
	select {
	case canceled := <-leader:
		if !errors.Is(canceled.err, context.Canceled) {
			t.Fatal("first HTTP caller did not cancel independently")
		}
	case <-time.After(time.Second):
		t.Fatal("first caller cancellation did not converge")
	}
	releaseOnce.Do(func() { close(release) })
	for i := 0; i < followers; i++ {
		select {
		case result := <-results:
			if result.err != nil || result.status != 200 || len(result.page.Items) != 2 || result.page.Items[0].Title != "缓存乙" || result.page.Items[0].Content != "公共正文完整" || len(result.page.Items[0].Images) != 2 {
				t.Fatal("shared real HTTP result was incomplete or canceled with first caller")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("shared HTTP followers did not converge")
		}
	}
	stats := f.cards.Stats()
	if !stats.Coalescing || stats.ContentSQLBatches != 1 || stats.ContentSQLRows != 2 || stats.FlightStarted != 1 || stats.FlightJoins != followers {
		t.Fatalf("same batch did not merge once: %+v", stats)
	}
	page, status := f.request(t, 1, "limit=2")
	if status != 200 || len(page.Items) != 2 || f.cards.Stats().ContentSQLBatches != 1 {
		t.Fatal("shared load did not populate correct hot cards")
	}
	t.Logf("real HTTP callers=%d canceled first=1 successful followers=%d; shared flights=%d joins=%d content SQL batches=%d rows=%d", followers+1, followers, stats.FlightStarted, stats.FlightJoins, stats.ContentSQLBatches, stats.ContentSQLRows)
}

func TestIntegrationFeedCardsHTTPProjectionCompleteness(t *testing.T) {
	f := newCardFeed(t)
	for _, id := range f.notes {
		if f.r.ORM.Exec(`INSERT INTO tb_community_note_projection
			(note_id,author_id,version,event_hash,last_event_id,published_at)
			SELECT id,user_id,publish_version,UNHEX(SHA2('cap07-owned-fixture',256)),CONCAT('cap07:',id),create_time
			FROM tb_blog WHERE id=?`, id).Error != nil {
			t.Fatal("owned catalog setup failed")
		}
	}
	f.start(t)
	projected, status := f.request(t, 0, "limit=2")
	if status != 200 || projected.Source != sourceProjection || len(projected.Items) != 2 {
		t.Fatal("complete projected narrow directory changed content")
	}
	if f.r.ORM.Exec("DELETE FROM tb_community_note_projection WHERE note_id=?", f.notes[0]).Error != nil {
		t.Fatal("owned catalog gap failed")
	}
	fallback, status := f.request(t, 0, "limit=2")
	if status != 200 || fallback.Source != sourceDatabase || len(fallback.Items) != 2 || fallback.Items[0].ID != projected.Items[0].ID || fallback.Items[1].ID != strconv.FormatUint(f.notes[0], 10) {
		t.Fatal("incomplete projection removed cached authorized content")
	}
	if f.cards.Stats().ContentSQLBatches != 1 {
		t.Fatal("projection source change needlessly reloaded same public body")
	}
}
