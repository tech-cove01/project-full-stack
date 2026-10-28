package notesearch

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/communityconsumer"
	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/segmentio/kafka-go"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This fixture owns private ES generations/topic and exact SQL rows. The named
// lock serializes GO-076 integration fixtures; no API/consumer may use the same
// dedicated dependency instance during this suite.
func TestIntegrationSearchSync(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL/Redis/Kafka/ES")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("explicit isolated configuration invalid")
	}
	if !cfg.Search.Ready || len(cfg.Kafka.Brokers) == 0 {
		t.Skip("ES_URL and KAFKA_BROKERS are required for full search synchronization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated SQL/Redis unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	connection, err := resources.SQL.Conn(ctx)
	if err != nil {
		t.Fatal("integration lock unavailable")
	}
	var acquired int
	if connection.QueryRowContext(ctx, "SELECT GET_LOCK('go076_integration',30)").Scan(&acquired) != nil || acquired != 1 {
		_ = connection.Close()
		t.Fatal("GO076 integration fixture already active")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = connection.ExecContext(cleanup, "SELECT RELEASE_LOCK('go076_integration')")
		_ = connection.Close()
	})
	f := newSearchSyncFixture(t, resources, cfg)
	runtime := NewSync(resources.ORM, f.client)
	if err = runtime.Rebuild(ctx); err != nil {
		t.Fatal("initial private generation rebuild failed")
	}
	note := f.note(t, 3, "同步版本三")
	message := f.message(note, 3)
	t.Run("duplicate-and-old-versions-use-current-published-fact", func(t *testing.T) {
		if err := runtime.Apply(ctx, Projection, message); err != nil {
			t.Fatal("initial latest fact could not apply")
		}
		otherAlias, _ := NewClient(Config{URL: cfg.Search.URL, Index: f.client.Index() + "-other", Timeout: cfg.Search.Timeout})
		defer otherAlias.CloseIdleConnections()
		if err := NewSync(resources.ORM, otherAlias).Apply(ctx, Projection, message); !errors.Is(err, ErrNeedsRebuild) {
			t.Fatal("a duplicate receipt hid a changed uninitialized search alias")
		}
		if resources.ORM.Exec(`UPDATE tb_blog SET title='同版本冲突正文' WHERE id=?`, note).Error != nil {
			t.Fatal("conflict fixture update failed")
		}
		if err := runtime.Apply(ctx, Projection, f.message(note, 2)); !errors.Is(err, ErrSyncConflict) {
			t.Fatal("same business version with a different document overwrote ES")
		}
		if f.indexed(t, note).Title != "同步版本三" {
			t.Fatal("conflicting same-version fact changed ES")
		}
		if resources.ORM.Exec(`UPDATE tb_blog SET title='同步版本三' WHERE id=?`, note).Error != nil {
			t.Fatal("fixture restore failed")
		}
		for _, version := range []int{3, 2, 1, 3} {
			if err := runtime.Apply(ctx, Projection, f.message(note, version)); err != nil {
				t.Fatalf("version application failed: %T", err)
			}
		}
		f.scalar(t, `SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id LIKE ?`, 3, Projection, "NOTE:"+note+":%")
		f.scalar(t, `SELECT version FROM tb_note_search_state WHERE note_id=?`, 3, note)
		doc := f.indexed(t, note)
		if doc.Version != 3 || doc.Title != "同步版本三" {
			t.Fatal("older event replaced latest fact")
		}
		conflict := message
		conflict.Body = []byte(strings.Replace(string(message.Body), `"authorId":"`+f.user+`"`, `"authorId":"999"`, 1))
		if err := runtime.Apply(ctx, Projection, conflict); !errors.Is(err, ErrSyncConflict) {
			t.Fatal("same event ID with different normalized payload was accepted")
		}
	})
	t.Run("ES-success-before-SQL-rollback-redelivers-once", func(t *testing.T) {
		crashNote := f.note(t, 1, "提交窗口测试")
		event, decodeErr := decodeSearchMessage(Projection, f.message(crashNote, 1))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		rollback := errors.New("controlled rollback after ES write")
		err := resources.ORM.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := runtime.apply(tx, event); err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal("controlled ES/SQL commit window was not reached")
		}
		if f.indexed(t, crashNote).Version != 1 {
			t.Fatal("ES write was not durable before rollback")
		}
		f.scalar(t, `SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?`, 0, Projection, event.EventID)
		f.scalar(t, `SELECT COUNT(*) FROM tb_note_search_state WHERE note_id=?`, 0, crashNote)
		if err = runtime.Apply(ctx, Projection, f.message(crashNote, 1)); err != nil {
			t.Fatal("same external version could not recover uncertain SQL outcome")
		}
		f.scalar(t, `SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?`, 1, Projection, event.EventID)
	})
	t.Run("ES-unreachable-parks-and-replays-original-source", func(t *testing.T) {
		f.update(t, note, 5, "故障恢复版本五")
		failed := f.message(note, 5)
		failed.Offset = 50
		broken, _ := NewClient(Config{URL: "http://127.0.0.1:1", Index: f.client.Index(), Timeout: time.Second})
		defer broken.CloseIdleConnections()
		unavailable := NewSync(resources.ORM, broken)
		if err := unavailable.Apply(ctx, Projection, failed); err == nil {
			t.Fatal("unreachable ES was mistaken for a successful index write")
		}
		if err := unavailable.Park(ctx, Projection, failed, "apply_failed"); err != nil {
			t.Fatal("ES outage did not have durable recovery destination")
		}
		mutated := failed
		mutated.Body = append(append([]byte(nil), failed.Body...), byte(' '))
		if err := unavailable.Park(ctx, Projection, mutated, "apply_failed"); !errors.Is(err, ErrSyncConflict) {
			t.Fatal("parked source was mutable at the same coordinate")
		}
		if err := runtime.Replay(ctx, failed.Topic, failed.Partition, failed.Offset); err != nil {
			t.Fatal("healthy ES could not replay parked source")
		}
		if err := runtime.Replay(ctx, failed.Topic, failed.Partition, failed.Offset); err != nil {
			t.Fatal("resolved source replay was not idempotent")
		}
		f.scalar(t, `SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection=? AND topic=? AND source_offset=50 AND state='RESOLVED'`, 1, Projection, f.topic)
		if f.indexed(t, note).Version != 5 {
			t.Fatal("replay did not restore latest published fact")
		}
	})
	t.Run("rebuild-snapshot-and-waiting-incremental-converge", func(t *testing.T) {
		previous, err := runtime.Stats(ctx)
		if err != nil {
			t.Fatal("stats unavailable")
		}
		f.update(t, note, 6, "全量快照版本六")
		gatedClient, _ := NewClient(Config{URL: cfg.Search.URL, Index: f.client.Index(), Timeout: cfg.Search.Timeout})
		original := gatedClient.http.Transport
		defer original.(*http.Transport).CloseIdleConnections()
		gate := &searchSyncGate{next: original, note: note, entered: make(chan struct{}), release: make(chan struct{})}
		gatedClient.http.Transport = gate
		defer func() {
			select {
			case <-gate.release:
			default:
				close(gate.release)
			}
		}()
		barrierRuntime := NewSync(resources.ORM, gatedClient)
		rebuilt := make(chan error, 1)
		go func() { rebuilt <- barrierRuntime.Rebuild(ctx) }()
		select {
		case <-gate.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("rebuild did not reach the actual ES snapshot write")
		}
		f.update(t, note, 7, "快照后增量版本七")
		applied := make(chan error, 1)
		go func() { applied <- runtime.Apply(ctx, Projection, f.message(note, 7)) }()
		select {
		case <-applied:
			close(gate.release)
			t.Fatal("incremental effect bypassed rebuild control barrier")
		case <-time.After(100 * time.Millisecond):
		}
		close(gate.release)
		if err = <-rebuilt; err != nil {
			t.Fatal("snapshot generation did not activate")
		}
		if err = <-applied; err != nil {
			t.Fatal("waiting incremental did not apply after alias swap")
		}
		current, err := runtime.Stats(ctx)
		if err != nil || current.ActiveGeneration == previous.ActiveGeneration {
			t.Fatal("rebuild did not move to a fresh physical generation")
		}
		if f.indexed(t, note).Version != 7 {
			t.Fatal("snapshot overwrote newer business version")
		}
		f.scalar(t, `SELECT version FROM tb_note_search_state WHERE note_id=?`, 7, note)
		_, status, err := f.client.request(ctx, http.MethodGet, "/"+previous.ActiveGeneration, nil)
		if err != nil || status != 200 {
			t.Fatal("controlled rebuild deleted the old rollback generation")
		}
		if current.Receipts < previous.Receipts || current.Resolved != previous.Resolved {
			t.Fatal("rebuild erased receipts or failure history")
		}
	})
	t.Run("irrelevant-known-events-have-durable-ignored-receipts", func(t *testing.T) {
		target := "18446744073709551615"
		eventID := "FOLLOW:" + f.user + ":" + target + ":1"
		f.ignored = append(f.ignored, eventID)
		message := communityprojection.Message{Topic: f.topic, Partition: 0, Offset: 60, Key: []byte(target), Body: []byte(fmt.Sprintf(`{"eventId":"%s","eventType":"FOLLOW_CHANGED","aggregateType":"FOLLOW","aggregateId":"%s","version":1,"payload":{"userId":"%s","followUserId":"%s","active":"true","version":1}}`, eventID, target, f.user, target))}
		if err := runtime.Apply(ctx, Projection, message); err != nil {
			t.Fatal("known irrelevant event did not produce durable ignored proof")
		}
		if err := runtime.Apply(ctx, Projection, message); err != nil {
			t.Fatal("ignored receipt was not idempotent")
		}
		f.scalar(t, `SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=? AND state='IGNORED'`, 1, Projection, eventID)
	})
	t.Run("Kafka-worker-commits-only-indexed-or-durably-parked-records", func(t *testing.T) {
		f.createTopic(t)
		f.update(t, note, 8, "真实消费版本八")
		good := f.message(note, 8)
		poison := f.message(note, 9)
		poison.Key = []byte("wrong-aggregate")
		outageNote := f.note(t, 1, "真实消费故障后重放")
		outage := f.message(outageNote, 1)
		transport := &kafka.Transport{DialTimeout: 2 * time.Second, MetadataTopics: []string{f.topic}}
		defer transport.CloseIdleConnections()
		writer := &kafka.Writer{Addr: kafka.TCP(cfg.Kafka.Brokers...), Topic: f.topic, RequiredAcks: kafka.RequireAll, MaxAttempts: 1, BatchTimeout: time.Millisecond, Transport: transport}
		defer func() { _ = writer.Close() }()
		if err := writer.WriteMessages(ctx, kafka.Message{Key: good.Key, Value: good.Body}, kafka.Message{Key: good.Key, Value: good.Body}, kafka.Message{Key: poison.Key, Value: poison.Body}, kafka.Message{Key: outage.Key, Value: outage.Body}); err != nil {
			t.Fatal("private Kafka source publication failed")
		}
		broken, _ := NewClient(Config{URL: "http://127.0.0.1:1", Index: f.client.Index(), Timeout: time.Second})
		defer broken.CloseIdleConnections()
		processor := searchSyncOutage{healthy: runtime, down: NewSync(resources.ORM, broken), note: outageNote}
		worker, err := communityconsumer.NewWorker(cfg.Kafka.Brokers, f.topic, Projection, processor)
		if err != nil {
			t.Fatal("search stable group constructor rejected")
		}
		defer func() { _ = worker.Close() }()
		for ctx.Err() == nil {
			processed, err := worker.Poll(ctx)
			if processed && err == nil && f.offset(t) == 4 {
				break
			}
		}
		if ctx.Err() != nil {
			t.Fatal("search group did not reach broker-confirmed offset four")
		}
		f.scalar(t, `SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?`, 1, Projection, "NOTE:"+note+":8:PUBLISHED")
		f.scalar(t, `SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection=? AND topic=? AND source_offset=2 AND state='PARKED'`, 1, Projection, f.topic)
		f.scalar(t, `SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection=? AND topic=? AND source_offset=3 AND state='PARKED'`, 1, Projection, f.topic)
		if f.indexed(t, note).Version != 8 {
			t.Fatal("committed Kafka message did not index actual business fact")
		}
		if err = runtime.Replay(ctx, f.topic, 0, 2); !errors.Is(err, ErrSyncInvalid) {
			t.Fatal("poison replay edited the original source into success")
		}
		if err = runtime.Replay(ctx, f.topic, 0, 3); err != nil || f.indexed(t, outageNote).Version != 1 {
			t.Fatal("actual Kafka ES failure could not replay after recovery")
		}
		if worker.Close() != nil {
			t.Fatal("first search consumer did not close")
		}
		// A closed independent pool is a real driver/database access failure;
		// unlike a durable park, it must not advance this source offset.
		closed, err := gorm.Open(gormmysql.New(gormmysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal("closed SQL fixture construction failed")
		}
		pool, err := closed.DB()
		if err != nil || pool.Close() != nil {
			t.Fatal("closed SQL fixture failed")
		}
		lastNote := f.note(t, 1, "数据库恢复重投")
		last := f.message(lastNote, 1)
		if writer.WriteMessages(ctx, kafka.Message{Key: last.Key, Value: last.Body}) != nil {
			t.Fatal("database-failure Kafka source failed")
		}
		downWorker, err := communityconsumer.NewWorker(cfg.Kafka.Brokers, f.topic, Projection, NewSync(closed, f.client))
		if err != nil {
			t.Fatal("SQL-down worker configuration failed")
		}
		attempts := 0
		for attempts < 3 && ctx.Err() == nil {
			processed, _ := downWorker.Poll(ctx)
			if processed {
				attempts++
			}
		}
		if attempts != 3 || f.offset(t) != 4 {
			t.Fatal("failed SQL persistence advanced a Kafka offset")
		}
		if downWorker.Close() != nil {
			t.Fatal("SQL-down consumer close failed")
		}
		recovered, err := communityconsumer.NewWorker(cfg.Kafka.Brokers, f.topic, Projection, runtime)
		if err != nil {
			t.Fatal("recovered consumer configuration failed")
		}
		defer func() { _ = recovered.Close() }()
		for ctx.Err() == nil {
			processed, err := recovered.Poll(ctx)
			if processed && err == nil && f.offset(t) == 5 {
				break
			}
		}
		if ctx.Err() != nil || f.indexed(t, lastNote).Version != 1 {
			t.Fatal("database recovery failed to redeliver the uncommitted source")
		}
	})
	t.Run("alias-activation-before-SQL-rollback-is-reconciled-by-rebuild", func(t *testing.T) {
		before, err := runtime.Stats(ctx)
		if err != nil {
			t.Fatal("pre-rebuild metadata unavailable")
		}
		rollback := errors.New("controlled rollback after successful alias activation")
		err = resources.ORM.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := NewSync(tx, f.client).Rebuild(ctx); err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal("alias/SQL ambiguity window was not reached")
		}
		after, err := runtime.Stats(ctx)
		if err != nil || after.ActiveGeneration != before.ActiveGeneration {
			t.Fatal("SQL metadata did not roll back")
		}
		raw, status, err := f.client.request(ctx, http.MethodGet, "/_alias/"+f.client.Index(), nil)
		var actual map[string]json.RawMessage
		if err != nil || status != 200 || json.Unmarshal(raw, &actual) != nil || len(actual) != 1 {
			t.Fatal("actual ES alias cannot be verified")
		}
		if _, stillOld := actual[before.ActiveGeneration]; stillOld {
			t.Fatal("alias did not persist independently of SQL rollback")
		}
		if err = runtime.Rebuild(ctx); err != nil {
			t.Fatal("controlled retry failed to reconcile uncertain rebuild")
		}
		final, err := runtime.Stats(ctx)
		if err != nil {
			t.Fatal("reconciled metadata unavailable")
		}
		raw, status, err = f.client.request(ctx, http.MethodGet, "/_alias/"+f.client.Index(), nil)
		actual = nil
		if err != nil || status != 200 || json.Unmarshal(raw, &actual) != nil || len(actual) != 1 {
			t.Fatal("reconciled actual alias invalid")
		}
		if _, matches := actual[final.ActiveGeneration]; !matches {
			t.Fatal("rebuild retry left alias and SQL metadata inconsistent")
		}
		if final.Receipts != before.Receipts || final.Parked != before.Parked || final.Resolved != before.Resolved {
			t.Fatal("uncertain rebuild recovery erased durable history")
		}
	})
}

type searchSyncOutage struct {
	healthy, down *Sync
	note          string
}

func (p searchSyncOutage) Apply(ctx context.Context, projection string, message communityprojection.Message) error {
	event, err := communityprojection.Decode(message.Body)
	if err == nil && event.NoteID == p.note {
		return p.down.Apply(ctx, projection, message)
	}
	return p.healthy.Apply(ctx, projection, message)
}
func (p searchSyncOutage) Park(ctx context.Context, projection string, message communityprojection.Message, reason string) error {
	return p.healthy.Park(ctx, projection, message, reason)
}

type searchSyncState struct {
	NoteID       uint64
	Version      int
	DocumentHash []byte
	AppliedAt    time.Time
}
type searchSyncFixture struct {
	resources            *database.Resources
	cfg                  config.Config
	client               *Client
	user, merchant, shop string
	notes                []string
	ignored              []string
	topic                string
	hasTopic             bool
	oldControl           searchControl
	oldState             []searchSyncState
}

func newSearchSyncFixture(t *testing.T, resources *database.Resources, cfg config.Config) *searchSyncFixture {
	t.Helper()
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("fixture entropy unavailable")
	}
	suffix := hex.EncodeToString(entropy[:])
	client, err := NewClient(Config{URL: cfg.Search.URL, Index: "go076-sync-" + suffix, Timeout: cfg.Search.Timeout})
	if err != nil {
		t.Fatal("private ES client invalid")
	}
	f := &searchSyncFixture{resources: resources, cfg: cfg, client: client, topic: "community.go076.sync." + suffix}
	if resources.ORM.Raw(`SELECT name,index_alias,active_generation,rebuilt_at FROM tb_note_search_control WHERE name=?`, Projection).Scan(&f.oldControl).Error != nil || f.oldControl.Name != Projection {
		t.Fatal("012 migration missing")
	}
	if resources.ORM.Raw(`SELECT note_id,version,document_hash,applied_at FROM tb_note_search_state`).Scan(&f.oldState).Error != nil {
		t.Fatal("search state snapshot unavailable")
	}
	t.Cleanup(func() { f.cleanup(t) })
	phone := fmt.Sprintf("135%08d", binary.BigEndian.Uint64(entropy[:])%100000000)
	f.user = syncInsertID(t, resources.ORM, `INSERT INTO tb_user(phone,nick_name) VALUES(?,'owned GO076 sync')`, phone)
	f.merchant = syncInsertID(t, resources.ORM, `INSERT INTO tb_merchant(name) VALUES(?)`, "owned GO076 sync "+suffix)
	f.shop = syncInsertID(t, resources.ORM, `INSERT INTO tb_shop(merchant_id,name,type_id,address,x,y) VALUES(?,'owned GO076 sync shop',1,'synthetic address',0,0)`, f.merchant)
	return f
}
func syncInsertID(t *testing.T, db *gorm.DB, query string, args ...any) string {
	t.Helper()
	var id uint64
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(query, args...).Error; err != nil {
			return err
		}
		return tx.Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error
	})
	if err != nil || id == 0 {
		t.Fatal("owned SQL fixture insert failed")
	}
	return strconv.FormatUint(id, 10)
}
func (f *searchSyncFixture) note(t *testing.T, version int, title string) string {
	t.Helper()
	id := syncInsertID(t, f.resources.ORM, `INSERT INTO tb_blog(shop_id,user_id,title,content,status,publish_version) VALUES(?,?,?,'公开笔记合成内容','PUBLISHED',?)`, f.shop, f.user, title, version)
	f.notes = append(f.notes, id)
	return id
}
func (f *searchSyncFixture) message(note string, version int) communityprojection.Message {
	message := syncNoteMessage(note, f.user, version)
	message.Topic = f.topic
	return message
}
func (f *searchSyncFixture) update(t *testing.T, note string, version int, title string) {
	t.Helper()
	if f.resources.ORM.Exec(`UPDATE tb_blog SET publish_version=?,title=? WHERE id=? AND user_id=?`, version, title, note, f.user).Error != nil {
		t.Fatal("owned note fact update failed")
	}
}
func (f *searchSyncFixture) scalar(t *testing.T, query string, want int64, args ...any) {
	t.Helper()
	var got int64
	if f.resources.ORM.Raw(query, args...).Scan(&got).Error != nil || got != want {
		t.Fatalf("durable SQL invariant wanted %d got %d", want, got)
	}
}
func (f *searchSyncFixture) indexed(t *testing.T, note string) Document {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, status, err := f.client.request(ctx, http.MethodGet, "/"+f.client.Index()+"/_doc/"+note, nil)
	var response struct {
		Source  Document `json:"_source"`
		Version int      `json:"_version"`
	}
	if err != nil || status != 200 || json.Unmarshal(raw, &response) != nil || response.Version != response.Source.Version {
		t.Fatal("authoritative real ES document unavailable")
	}
	return response.Source
}
func (f *searchSyncFixture) createTopic(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connection, err := (&kafka.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", f.cfg.Kafka.Brokers[0])
	if err != nil {
		t.Fatal("isolated Kafka unavailable")
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if connection.CreateTopics(kafka.TopicConfig{Topic: f.topic, NumPartitions: 1, ReplicationFactor: 1}) != nil {
		t.Fatal("private topic creation failed")
	}
	f.hasTopic = true
	for ctx.Err() == nil {
		leader, err := (&kafka.Dialer{Timeout: 2 * time.Second}).DialLeader(ctx, "tcp", f.cfg.Kafka.Brokers[0], f.topic, 0)
		if err == nil {
			_ = leader.SetDeadline(time.Now().Add(2 * time.Second))
			last, err := leader.ReadLastOffset()
			_ = leader.Close()
			if err == nil && last == 0 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("private topic leader not ready")
}
func (f *searchSyncFixture) offset(t *testing.T) int64 {
	t.Helper()
	transport := &kafka.Transport{DialTimeout: 2 * time.Second}
	defer transport.CloseIdleConnections()
	client := &kafka.Client{Addr: kafka.TCP(f.cfg.Kafka.Brokers...), Timeout: 5 * time.Second, Transport: transport}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: "community." + Projection, Topics: map[string][]int{f.topic: {0}}})
	if err != nil || response.Error != nil || len(response.Topics[f.topic]) != 1 || response.Topics[f.topic][0].Error != nil {
		t.Fatal("authoritative search-group offset unavailable")
	}
	return response.Topics[f.topic][0].CommittedOffset
}

type searchSyncGate struct {
	next             http.RoundTripper
	note             string
	once             sync.Once
	entered, release chan struct{}
}

func (g *searchSyncGate) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPut && strings.Contains(request.URL.Path, "-build-") && strings.HasSuffix(request.URL.Path, "/_doc/"+g.note) {
		g.once.Do(func() {
			close(g.entered)
			select {
			case <-g.release:
			case <-request.Context().Done():
			}
		})
	}
	return g.next.RoundTrip(request)
}
func (f *searchSyncFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	defer f.client.CloseIdleConnections()
	err := f.resources.ORM.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockSearchControl(tx); err != nil {
			return err
		}
		for _, note := range f.notes {
			if err := tx.Exec(`DELETE FROM tb_community_projection_event WHERE projection=? AND event_id LIKE ?`, Projection, "NOTE:"+note+":%").Error; err != nil {
				return err
			}
			if err := tx.Exec(`DELETE FROM tb_blog WHERE id=? AND user_id=?`, note, f.user).Error; err != nil {
				return err
			}
		}
		if len(f.ignored) > 0 {
			if err := tx.Exec(`DELETE FROM tb_community_projection_event WHERE projection=? AND event_id IN ?`, Projection, f.ignored).Error; err != nil {
				return err
			}
		}
		for _, deletion := range []struct {
			query string
			args  []any
		}{
			{`DELETE FROM tb_community_consumer_failure WHERE projection=? AND topic=?`, []any{Projection, f.topic}},
			{`DELETE FROM tb_shop WHERE id=? AND merchant_id=?`, []any{f.shop, f.merchant}},
			{`DELETE FROM tb_merchant WHERE id=?`, []any{f.merchant}},
			{`DELETE FROM tb_user WHERE id=?`, []any{f.user}},
			{`DELETE FROM tb_note_search_state`, nil},
		} {
			if err := tx.Exec(deletion.query, deletion.args...).Error; err != nil {
				return err
			}
		}
		for _, row := range f.oldState {
			if err := tx.Exec(`INSERT INTO tb_note_search_state(note_id,version,document_hash,applied_at) VALUES(?,?,?,?)`, row.NoteID, row.Version, row.DocumentHash, row.AppliedAt).Error; err != nil {
				return err
			}
		}
		return tx.Exec(`UPDATE tb_note_search_control SET index_alias=?,active_generation=?,rebuilt_at=? WHERE name=?`, f.oldControl.IndexAlias, f.oldControl.ActiveGeneration, f.oldControl.RebuiltAt, Projection).Error
	})
	if err != nil {
		t.Error("exact owned SQL cleanup/control restore failed")
	}
	// Only names under this randomly generated private alias are removed.
	raw, status, err := f.client.request(ctx, http.MethodGet, "/_cat/indices/"+f.client.Index()+"-build-*?format=json", nil)
	var indices []struct {
		Index string `json:"index"`
	}
	if err == nil && status == 200 && json.Unmarshal(raw, &indices) == nil {
		for _, index := range indices {
			if !f.client.generation(index.Index) {
				t.Error("private index cleanup ownership check failed")
				continue
			}
			_, status, err = f.client.request(ctx, http.MethodDelete, "/"+index.Index, nil)
			if err != nil || status != 200 {
				t.Error("private ES generation cleanup failed")
			}
		}
	} else {
		t.Error("private ES generation discovery failed")
	}
	if f.hasTopic {
		connection, err := (&kafka.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", f.cfg.Kafka.Brokers[0])
		if err != nil {
			t.Error("private topic cleanup connection failed")
		} else {
			_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
			if connection.DeleteTopics(f.topic) != nil {
				t.Error("private topic cleanup failed")
			}
			_ = connection.Close()
		}
	}
}
