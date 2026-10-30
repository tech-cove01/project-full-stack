package communitycards

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type integrationFixture struct {
	ctx    context.Context
	r      *database.Resources
	ref    Ref
	caches []*Cache
}

func integrationCards(t *testing.T) *integrationFixture {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	r, err := database.Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal("isolated dependencies unavailable")
	}
	f := &integrationFixture{ctx: ctx, r: r}
	t.Cleanup(func() {
		for _, cache := range f.caches {
			if cache.Close() != nil {
				t.Error("owned cache close failed")
			}
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if f.ref.ID != 0 {
			keys := []string{key(f.ref), key(Ref{f.ref.ID, 1}), key(Ref{f.ref.ID, 2}), key(f.ref) + ":blocked"}
			if r.Redis.Del(cleanup, keys...).Err() != nil {
				t.Error("owned Redis key cleanup failed")
			}
			if count, err := r.Redis.Exists(cleanup, keys...).Result(); err != nil || count != 0 {
				t.Error("owned Redis cleanup left a fixture key")
			}
			if r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_blog WHERE id=?", f.ref.ID).Error != nil {
				t.Error("owned note cleanup failed")
			}
		}
		if r.Close() != nil {
			t.Error("dependency cleanup failed")
		}
		cancel()
	})
	row := struct {
		ID, ShopID, UserID, PublishVersion uint64
		Title, Content, Images, Status     string
	}{UserID: 1, Title: "公共卡片真实夹具", Content: "完整正文", Images: " a.png, ,b.png ", Status: "PUBLISHED"}
	if err := r.ORM.WithContext(ctx).Table("tb_blog").Create(&row).Error; err != nil || row.ID == 0 {
		t.Fatal("owned note insert failed")
	}
	f.ref = Ref{ID: row.ID, Version: 0}
	return f
}

func (f *integrationFixture) cache(db *gorm.DB) *Cache {
	cache := New(f.ctx, f.r.Redis, db)
	f.caches = append(f.caches, cache)
	return cache
}

func TestIntegrationCardsRedisBadPayloadAndType(t *testing.T) {
	f := integrationCards(t)
	for _, kind := range []string{"bad-json", "null-image", "wrong-type", "oversized-payload"} {
		t.Run(kind, func(t *testing.T) {
			if f.r.Redis.Del(f.ctx, key(f.ref)).Err() != nil {
				t.Fatal("owned key reset failed")
			}
			var err error
			switch kind {
			case "wrong-type":
				err = f.r.Redis.LPush(f.ctx, key(f.ref), "owned wrong Redis type").Err()
			case "bad-json":
				err = f.r.Redis.Set(f.ctx, key(f.ref), "{", time.Minute).Err()
			case "oversized-payload":
				raw, encodeErr := encode(f.ref, Card{Content: strings.Repeat("大", 8192)})
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				err = f.r.Redis.Set(f.ctx, key(f.ref), raw, time.Minute).Err()
			case "null-image":
				raw, encodeErr := encode(f.ref, Card{Images: []string{"replace"}})
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				err = f.r.Redis.Set(f.ctx, key(f.ref), strings.Replace(string(raw), `["replace"]`, `[null]`, 1), time.Minute).Err()
			}
			if err != nil {
				t.Fatal("owned malformed payload setup failed")
			}
			cache := f.cache(f.r.ORM)
			result, err := cache.GetMany(f.ctx, []Ref{f.ref})
			if err != nil || result[f.ref].Content != "完整正文" || len(result[f.ref].Images) != 2 || result[f.ref].Images[0] != "a.png" {
				t.Fatal("real MGET bad value did not rebuild complete content")
			}
			stats := cache.Stats()
			if stats.ContentSQLBatches != 1 || stats.ContentSQLRows != 1 || stats.RedisHits != 0 {
				t.Fatalf("bad Redis value was accepted: %#v", stats)
			}
			raw, err := f.r.Redis.Get(f.ctx, key(f.ref)).Bytes()
			if _, valid := decode(raw, f.ref, defaults().maxCardBytes); err != nil || !valid {
				t.Fatal("real pipeline failed to replace malformed/nonstring value")
			}
		})
	}
}

func TestIntegrationCardsOwnedRedisExpiryRebuilds(t *testing.T) {
	f := integrationCards(t)
	first := f.cache(f.r.ORM)
	if _, err := first.GetMany(f.ctx, []Ref{f.ref}); err != nil {
		t.Fatal("owned card cold load failed")
	}
	// Only this fixture key gets a short server TTL. Production defaults stay
	// unchanged; a fresh process cache avoids the intentionally warm L1.
	if err := f.r.Redis.PExpire(f.ctx, key(f.ref), 30*time.Millisecond).Err(); err != nil {
		t.Fatal("owned expiry setup failed")
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		exists, err := f.r.Redis.Exists(f.ctx, key(f.ref)).Result()
		if err != nil {
			t.Fatal("owned expiry observation failed")
		}
		if exists == 0 {
			break
		}
		select {
		case <-timer.C:
			t.Fatal("server did not naturally expire the owned key")
		case <-time.After(5 * time.Millisecond):
		}
	}
	second := f.cache(f.r.ORM)
	result, err := second.GetMany(f.ctx, []Ref{f.ref})
	if err != nil || result[f.ref].Content != "完整正文" || second.Stats().ContentSQLBatches != 1 {
		t.Fatal("real expired Redis key did not rebuild through content SQL")
	}
	if ttl, err := f.r.Redis.PTTL(f.ctx, key(f.ref)).Result(); err != nil || ttl < 55*time.Second || ttl >= 69*time.Second {
		t.Fatal("rebuild changed the fixed production TTL range")
	}
}

func TestIntegrationCardsDisconnectedRedisUsesRealSQL(t *testing.T) {
	f := integrationCards(t)
	options := *f.r.Redis.Options()
	client := redis.NewClient(&options)
	if client.Close() != nil {
		t.Fatal("owned disconnected Redis client setup failed")
	}
	cache := New(f.ctx, client, f.r.ORM)
	f.caches = append(f.caches, cache)
	result, err := cache.GetMany(f.ctx, []Ref{f.ref})
	if err != nil || result[f.ref].Content != "完整正文" || len(result[f.ref].Images) != 2 {
		t.Fatal("disconnected Redis prevented complete real SQL result")
	}
	if stats := cache.Stats(); stats.ContentSQLBatches != 1 || stats.ContentSQLRows != 1 || stats.RedisErrors != 3 || stats.L1Entries != 1 || stats.L1Bytes == 0 {
		t.Fatalf("disconnected Redis did not use one bounded content query: %#v", stats)
	}
	if result, err := cache.GetMany(f.ctx, []Ref{f.ref}); err != nil || result[f.ref].Content != "完整正文" || cache.Stats().ContentSQLBatches != 1 {
		t.Fatal("successful degraded load did not retain a complete local card")
	}
}

func TestIntegrationCardsUnavailableVersionAndSQLFailureDoNotCacheEmpty(t *testing.T) {
	f := integrationCards(t)
	cache := f.cache(f.r.ORM)
	missing := Ref{ID: f.ref.ID, Version: 1}
	if result, err := cache.GetMany(f.ctx, []Ref{f.ref, missing}); !errors.Is(err, ErrVersionChanged) || result != nil {
		t.Fatal("real SQL version miss returned a partial/newer body")
	}
	if cache.Stats().ContentSQLRows != 1 || cache.Stats().L1Entries != 0 {
		t.Fatal("real version miss cached a partial body")
	}
	if count, err := f.r.Redis.Exists(f.ctx, key(f.ref), key(missing)).Result(); err != nil || count != 0 {
		t.Fatal("version miss published a Redis card")
	}
	// A separate ORM callback redirects only this component's actual content
	// statement to an absent table, exercising a real MySQL query error.
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: f.r.SQL, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("owned error-path ORM setup failed")
	}
	if db.Callback().Row().Before("gorm:row").Register("cap07_cards_owned_sql_error", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "SELECT id,publish_version,title,content,images FROM tb_blog") {
			tx.Statement.SQL.Reset()
			tx.Statement.SQL.WriteString("SELECT id FROM cap07_cards_absent_owned_table")
		}
	}) != nil {
		t.Fatal("owned SQL error callback setup failed")
	}
	failed := f.cache(db)
	if result, err := failed.GetMany(f.ctx, []Ref{f.ref}); err == nil || errors.Is(err, ErrVersionChanged) || result != nil {
		t.Fatal("real database error was masked")
	}
	if failed.Stats().ContentSQLBatches != 1 || failed.Stats().L1Entries != 0 {
		t.Fatal("real database error cached an empty body")
	}
	if count, err := f.r.Redis.Exists(f.ctx, key(f.ref)).Result(); err != nil || count != 0 {
		t.Fatal("database error published a Redis card")
	}
}

type slowOwnedPipeline struct {
	ownedKey, blockedKey, clientName string
	reached                          chan struct{}
	returned                         chan error
	used                             atomic.Bool
}

func (hook *slowOwnedPipeline) DialHook(next redis.DialHook) redis.DialHook { return next }
func (hook *slowOwnedPipeline) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return next
}
func (hook *slowOwnedPipeline) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error {
		for _, command := range commands {
			args := command.Args()
			if command.Name() == "set" && len(args) > 1 && args[1] == hook.ownedKey && hook.used.CompareAndSwap(false, true) {
				// This is a real server-blocking command on a unique empty fixture
				// key, inserted on this client connection. No server-wide pause,
				// cache timeout change or fake backend replaces Redis I/O.
				name := redis.NewStatusCmd(ctx, "client", "setname", hook.clientName)
				block := redis.NewStringSliceCmd(ctx, "blpop", hook.blockedKey, 1)
				withBlock := append([]redis.Cmder{name, block}, commands...)
				close(hook.reached)
				err := next(ctx, withBlock)
				hook.returned <- err
				return err
			}
		}
		return next(ctx, commands)
	}
}

func TestIntegrationCardsRealSlowRedisPipelineCloseConverges(t *testing.T) {
	f := integrationCards(t)
	hook := &slowOwnedPipeline{ownedKey: key(f.ref), blockedKey: key(f.ref) + ":blocked", clientName: "cap07-cards-" + strconv.FormatUint(f.ref.ID, 10), reached: make(chan struct{}), returned: make(chan error, 1)}
	options := *f.r.Redis.Options()
	client := redis.NewClient(&options)
	client.AddHook(hook)
	cache := New(f.ctx, client, f.r.ORM)
	f.caches = append(f.caches, cache)
	t.Cleanup(func() {
		_ = cache.Close()
		_ = client.Close()
		// A timed-out server BLPOP could otherwise process the queued SET
		// after deletion. Wait for this owned connection to disappear before
		// the fixture's earlier registered exact-key cleanup executes.
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		for {
			clients, err := f.r.Redis.ClientList(cleanup).Result()
			if err != nil {
				t.Error("owned pipeline cleanup observation failed")
				return
			}
			if !strings.Contains(clients, "name="+hook.clientName+" ") {
				return
			}
			select {
			case <-cleanup.Done():
				t.Error("owned Redis pipeline connection survived client cleanup")
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	done := make(chan error, 1)
	go func() { _, err := cache.GetMany(f.ctx, []Ref{f.ref}); done <- err }()
	await(t, hook.reached)
	// Observe this exact connection blocked in the server before cancellation;
	// a hook reached before any socket write is not real slow-Redis evidence.
	observed := false
	bound := time.NewTimer(time.Second)
	defer bound.Stop()
	for !observed {
		clients, err := f.r.Redis.ClientList(f.ctx).Result()
		if err != nil {
			t.Fatal("owned blocked connection observation failed")
		}
		for _, line := range strings.Split(clients, "\n") {
			fields := " " + line + " "
			if strings.Contains(fields, " name="+hook.clientName+" ") && strings.Contains(fields, " cmd=blpop ") {
				observed = true
			}
		}
		if !observed {
			select {
			case <-bound.C:
				t.Fatal("owned pipeline was not observed blocked in real Redis")
			case <-time.After(time.Millisecond):
			}
		}
	}
	closed := make(chan error, 1)
	go func() { closed <- cache.Close() }()
	if err := await(t, done); !errors.Is(err, ErrClosed) {
		t.Fatalf("close failed to cancel a caller during real Redis pipeline: %v", err)
	}
	if err := await(t, closed); err != nil {
		t.Fatal("cache close failed")
	}
	if err := await(t, hook.returned); err == nil {
		t.Fatal("slow real Redis pipeline escaped its bounded canceled operation")
	}
	if stats := cache.Stats(); stats.ContentSQLBatches != 1 || stats.RedisErrors != 1 || stats.Inflight != 0 || stats.ActiveLoads != 0 || stats.WaitingCallers != 0 {
		t.Fatalf("real slow pipeline left shared work/admission behind: %#v", stats)
	}
}
