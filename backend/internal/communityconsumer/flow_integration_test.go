package communityconsumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"github.com/cityloop/fullstack-go/internal/communityrelay"
	"github.com/cityloop/fullstack-go/internal/note"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
	"github.com/cityloop/fullstack-go/internal/social"
)

type flowReply struct {
	status  int
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// HTTP handlers use the real DB/Redis identity and business services. The test
// sends no fabricated successful responses and uses no external API process.
// Its private Kafka topic/groups and exact SQL fixture are cleaned independently
// of the shared community.events topic and production group offsets.
func TestIntegrationCommunityFlow(t *testing.T) {
	db, cfg := consumerDatabase(t) // Includes the fixed go072_integration lock.
	f := newConsumerFixture(t, db, cfg)
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	// Relay Claim is global to the community outbox. Refuse to run if any other
	// producer has pending/leased work instead of publishing their events into
	// this private topic. All cooperating suites hold the same fixture lock.
	f.scalar(t, `SELECT COUNT(*) FROM tb_community_outbox WHERE delivery_status IN ('PENDING','PROCESSING')`, 0)
	client := redis.NewClient(&redis.Options{Addr: net.JoinHostPort(cfg.Redis.Host, strconv.Itoa(cfg.Redis.Port)), Password: cfg.Redis.Password, DB: cfg.Redis.Database,
		DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, ContextTimeoutEnabled: true, MaxRetries: -1})
	t.Cleanup(func() {
		if client.Close() != nil {
			t.Error("cannot close owned identity Redis client")
		}
	})
	if client.Ping(ctx).Err() != nil {
		t.Fatal("isolated Redis identity dependency unavailable")
	}
	identity := auth.NewService(db, client, "development")
	router := httpapi.NewRouter(nil, time.Second, "go072-flow", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
		auth.Register(routes, identity)
		note.Register(routes, note.NewService(db), identity)
		social.Register(routes, social.NewService(db).WithProjections(), identity)
	})
	call := func(method, path, token string, body any) flowReply {
		t.Helper()
		var input io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal("cannot encode owned HTTP request")
			}
			input = bytes.NewReader(encoded)
		}
		request := httptest.NewRequest(method, path, input).WithContext(ctx)
		if token != "" {
			request.Header.Set("authorization", token)
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		var reply flowReply
		if json.Unmarshal(response.Body.Bytes(), &reply) != nil {
			t.Fatal("real handler returned an invalid JSON envelope")
		}
		reply.status = response.Code
		return reply
	}
	phones := []string{}
	tokens := []string{}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		keys := []string{}
		for _, phone := range phones {
			keys = append(keys, "login:code:"+phone, "login:code:throttle:"+phone)
		}
		for _, token := range tokens {
			keys = append(keys, "login:token:"+token)
		}
		if len(keys) > 0 {
			if client.Del(cleanup, keys...).Err() != nil {
				t.Error("cannot remove exact owned authentication keys")
			}
			if remaining, err := client.Exists(cleanup, keys...).Result(); err != nil || remaining != 0 {
				t.Error("owned authentication keys remain")
			}
		}
	})
	login := func(user string) string {
		t.Helper()
		var phone string
		if db.Raw("SELECT phone FROM tb_user WHERE id=?", user).Scan(&phone).Error != nil || phone == "" {
			t.Fatal("owned login identity not found")
		}
		phones = append(phones, phone)
		sent := call(http.MethodPost, "/user/code?phone="+phone, "", nil)
		var code auth.DevelopmentCode
		if sent.status != 200 || !sent.Success || json.Unmarshal(sent.Data, &code) != nil || code.Code == "" {
			t.Fatal("real development verification-code request failed")
		}
		reply := call(http.MethodPost, "/user/login", "", auth.LoginForm{Phone: phone, Code: code.Code})
		var token string
		if reply.status != 200 || !reply.Success || json.Unmarshal(reply.Data, &token) != nil || len(token) != 32 {
			t.Fatal("real verification-code login failed")
		}
		tokens = append(tokens, token)
		me := call(http.MethodGet, "/user/me", token, nil)
		var actual auth.User
		if me.status != 200 || !me.Success || json.Unmarshal(me.Data, &actual) != nil || actual.ID != user {
			t.Fatal("real login did not resolve the intended owned user")
		}
		return token
	}
	readerToken, authorToken := login(f.user), login(f.target)
	var historical string
	// The reusable fixture removes note/user/shop rows and receipts. This flow
	// additionally creates source facts/outbox rows through HTTP and owns them.
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		// A handler can commit before the caller sees a transport/storage error.
		// Enumerate our exact private author/note source identities again so a
		// partially completed flow does not leave its own relay work behind.
		var committed []string
		if db.WithContext(cleanup).Raw(`SELECT event_id FROM tb_community_outbox
 WHERE (aggregate_type='BLOG' AND aggregate_id=?) OR (aggregate_type='FOLLOW' AND aggregate_id=?)`, f.note, f.target).Scan(&committed).Error != nil {
			t.Error("cannot enumerate exact owned source cleanup")
		}
		seen := map[string]bool{}
		for _, id := range f.events {
			seen[id] = true
		}
		for _, id := range committed {
			if !seen[id] {
				f.events = append(f.events, id)
			}
		}
		for _, deletion := range []struct {
			query string
			args  []any
		}{
			{"DELETE FROM tb_community_outbox WHERE event_id IN ?", []any{f.events}},
			{"DELETE FROM tb_blog_like WHERE blog_id=? AND user_id=?", []any{f.note, f.user}},
			{"DELETE FROM tb_follow WHERE user_id=? AND follow_user_id=?", []any{f.user, f.target}},
		} {
			if db.WithContext(cleanup).Exec(deletion.query, deletion.args...).Error != nil {
				t.Error("cannot delete exact owned HTTP community facts")
			}
		}
		if historical != "" {
			if db.WithContext(cleanup).Exec("DELETE FROM tb_blog WHERE id=? AND user_id=?", historical, f.target).Error != nil {
				t.Error("cannot delete exact owned historical note")
			}
		}
	})
	created := call(http.MethodPost, "/notes", authorToken, note.CreateRequest{ShopID: f.shop, Title: "GO072真实消费贯通笔记", Content: "真实登录、事务事件、Kafka双投影与当前事实回退。", Images: []string{}})
	var published note.View
	if created.status != 200 || !created.Success || json.Unmarshal(created.Data, &published) != nil || published.ID == "" || published.UserID != f.target || published.Title != "GO072真实消费贯通笔记" {
		t.Fatal("real note handler did not create its committed public note")
	}
	f.note, f.hasNote = published.ID, true
	appendOwnedEvents := func() {
		t.Helper()
		var ids []string
		if db.WithContext(ctx).Raw(`SELECT event_id FROM tb_community_outbox
 WHERE (aggregate_type='BLOG' AND aggregate_id=?) OR (aggregate_type='FOLLOW' AND aggregate_id=?)`, f.note, f.target).Scan(&ids).Error != nil {
			t.Fatal("cannot enumerate exact owned source events")
		}
		known := make(map[string]bool, len(f.events))
		for _, id := range f.events {
			known[id] = true
		}
		for _, id := range ids {
			if !known[id] {
				f.events = append(f.events, id)
			}
		}
	}
	appendOwnedEvents()
	followed := call(http.MethodPut, "/authors/"+f.target+"/follow", readerToken, map[string]bool{"follow": true})
	var following social.Relationship
	if followed.status != 200 || !followed.Success || json.Unmarshal(followed.Data, &following) != nil || !following.Following || following.Version != 1 || following.Source != "database" {
		t.Fatal("real first follow did not return current DB facts before the consumer")
	}
	liked := call(http.MethodPut, "/notes/"+f.note+"/like", readerToken, map[string]bool{"liked": true})
	var like social.LikeState
	if liked.status != 200 || !liked.Success || json.Unmarshal(liked.Data, &like) != nil || !like.Liked || like.Version != 1 {
		t.Fatal("real desired-state like did not commit its event")
	}
	appendOwnedEvents()
	feed := func(source string, expected []string) social.FeedPage {
		t.Helper()
		reply := call(http.MethodGet, "/feed/notes?limit=10", readerToken, nil)
		var page social.FeedPage
		if reply.status != 200 || !reply.Success || json.Unmarshal(reply.Data, &page) != nil || page.Source != source || page.Items == nil || len(page.Items) != len(expected) {
			t.Fatal("real feed handler did not return the expected source/content")
		}
		for index, id := range expected {
			if page.Items[index].ID != id || page.Items[index].UserID != f.target {
				t.Fatal("real feed omitted, reordered or exposed a foreign author's note")
			}
			if id == f.note && (page.Items[index].Title != published.Title || !page.Items[index].LikedByMe || page.Items[index].Liked != 1) {
				t.Fatal("feed content/current-user like did not match committed facts")
			}
		}
		return page
	}
	feed("database", []string{f.note}) // First follow immediately sees existing notes.
	f.scalar(t, "SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN ? AND delivery_status='PENDING'", 3, f.events)
	f.scalar(t, "SELECT COUNT(*) FROM tb_community_outbox WHERE delivery_status IN ('PENDING','PROCESSING') AND event_id NOT IN ?", 0, f.events)
	topic := f.topic(t)
	relay := communityrelay.NewWorker(db, cfg.Kafka.Brokers, topic)
	t.Cleanup(func() {
		if relay.Close() != nil {
			t.Error("owned relay writer did not close")
		}
	})
	relayOwned := func(number int) {
		t.Helper()
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_outbox WHERE delivery_status IN ('PENDING','PROCESSING') AND event_id NOT IN ?", 0, f.events)
		for range number {
			processed, err := relay.Poll(ctx)
			if err != nil || !processed {
				t.Fatal("real outbox relay did not publish the expected owned event")
			}
		}
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN ? AND delivery_status='DONE'", int64(len(f.events)), f.events)
	}
	relayOwned(3)
	firstConfirmed := time.Now()
	followGroup, feedGroup := topic+".http-follow", topic+".http-feed"
	followWorker := f.worker(t, topic, communityprojection.Follow, followGroup, f.runtime)
	feedWorker := f.worker(t, topic, communityprojection.Feed, feedGroup, f.runtime)
	drainBoth := func(messages int, expectedOffset int64) {
		t.Helper()
		var wait sync.WaitGroup
		failures := make(chan error, 2)
		for _, worker := range []*Worker{followWorker, feedWorker} {
			wait.Add(1)
			go func(worker *Worker) {
				defer wait.Done()
				drainCtx, stop := context.WithTimeout(ctx, 35*time.Second)
				defer stop()
				committed := 0
				for committed < messages && drainCtx.Err() == nil {
					processed, err := worker.Poll(drainCtx)
					if err != nil {
						failures <- errors.New("real projection consumer did not confirm its effect and offset")
						return
					}
					if processed {
						committed++
					}
				}
				if committed != messages {
					failures <- errors.New("real projection consumer did not reach its owned source count")
				}
			}(worker)
		}
		wait.Wait()
		close(failures)
		for failure := range failures {
			if failure != nil {
				t.Fatal("real dual consumers did not finish their private-topic batch")
			}
		}
		if f.offset(t, topic, followGroup) != expectedOffset || f.offset(t, topic, feedGroup) != expectedOffset {
			t.Fatal("private broker offsets did not independently confirm both group batches")
		}
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=?", 0, topic)
	}
	drainBoth(3, 3)
	feed("projection", []string{f.note})
	relationship := call(http.MethodGet, "/authors/"+f.target+"/relationship", readerToken, nil)
	var state social.Relationship
	if relationship.status != 200 || !relationship.Success || json.Unmarshal(relationship.Data, &state) != nil || state.Source != "projection" || !state.Following || state.Version != 1 || state.FollowerCount != 1 {
		t.Fatal("real relationship handler did not use the completed follow read model")
	}
	for _, check := range []struct {
		projection, state string
		count             int64
	}{{communityprojection.Follow, "APPLIED", 1}, {communityprojection.Follow, "IGNORED", 2}, {communityprojection.Feed, "APPLIED", 1}, {communityprojection.Feed, "IGNORED", 2}} {
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id IN ? AND state=?", check.count, check.projection, f.events, check.state)
	}
	t.Logf("stage=http-to-kafka-dual-projection topic=%s note/follow/like=3 followNextOffset=3 feedNextOffset=3 receiptsEach=3 apply/ignore=1/2 source=projection startedAt=%s brokerConfirmedAt=%s observedAt=%s observedElapsedMs=%d",
		topic, started.UTC().Format(time.RFC3339Nano), firstConfirmed.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), time.Since(started).Milliseconds())
	// A pre-event-history row is intentionally inserted only in the DB. A partly
	// populated catalog must not make this older, public followed note disappear.
	historical = strconv.FormatUint(f.base+30, 10)
	var existing int64
	if db.Raw("SELECT COUNT(*) FROM tb_blog WHERE id=?", historical).Scan(&existing).Error != nil || existing != 0 {
		historical = ""
		t.Fatal("cannot establish exact historical fixture ownership")
	}
	if db.Exec(`INSERT INTO tb_blog(id,shop_id,user_id,title,images,content,status,publish_version,create_time)
 VALUES(?,?,?,'GO072历史无事件笔记','','owned historical fixture','PUBLISHED',1,DATE_SUB(NOW(),INTERVAL 1 DAY))`, historical, f.shop, f.target).Error != nil {
		historical = ""
		t.Fatal("cannot insert exact historical no-event note")
	}
	feed("database", []string{f.note, historical})
	f.scalar(t, "SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id=?", 0, historical)
	t.Log("stage=historical-gap current-public-notes=2 catalog-notes=1 source=database no-note-omitted=true")
	// Stop calling Poll at offset 3, then commit an HTTP unfollow. Reads must
	// hide this author immediately without waiting for the delayed graph event.
	unfollowed := call(http.MethodPut, "/authors/"+f.target+"/follow", readerToken, map[string]bool{"follow": false})
	if unfollowed.status != 200 || !unfollowed.Success || json.Unmarshal(unfollowed.Data, &state) != nil || state.Following || state.Version != 2 || state.Source != "database" {
		t.Fatal("HTTP unfollow did not return the current fact while its projection lagged")
	}
	appendOwnedEvents()
	feed("database", []string{})
	f.scalar(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND active=1 AND version=1", 1, f.user, f.target)
	if f.offset(t, topic, followGroup) != 3 || f.offset(t, topic, feedGroup) != 3 {
		t.Fatal("an unpolled consumer advanced before the controlled unfollow check")
	}
	relayOwned(1)
	drainBoth(1, 4)
	relationship = call(http.MethodGet, "/authors/"+f.target+"/relationship", readerToken, nil)
	if relationship.status != 200 || !relationship.Success || json.Unmarshal(relationship.Data, &state) != nil || state.Source != "projection" || state.Following || state.Version != 2 || state.FollowerCount != 0 {
		t.Fatal("dual consumers did not converge to the committed unfollow")
	}
	feed("database", []string{})
	f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id IN ?", 4, communityprojection.Follow, f.events)
	f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id IN ?", 4, communityprojection.Feed, f.events)
	f.scalar(t, "SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN ? AND delivery_status<>'DONE'", 0, f.events)
	f.scalar(t, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=?", 0, topic)
	t.Logf("stage=current-unfollow-guard-before-consume-and-recovery topic=%s laggingGraphVersion=1 committedFactVersion=2 feedBefore=[] graphAfter=inactive/v2 followNextOffset=4 feedNextOffset=4", topic)
	t.Logf("stage=final-accounting ownedOutboxPending=0 ownedParked=0 receiptsEach=4 firstConfirmedAt=%s completedAt=%s totalObservedElapsedMs=%d measurement=functional-observation-not-throughput-or-SLA",
		firstConfirmed.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), time.Since(started).Milliseconds())
}
