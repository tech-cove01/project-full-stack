package communityconsumer

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func consumerDatabase(t *testing.T) (*gorm.DB, config.Config) {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Kafka")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("explicit isolated MySQL configuration is required")
	}
	if len(cfg.Kafka.Brokers) == 0 {
		t.Skip("KAFKA_BROKERS is not configured")
	}
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot initialize consumer integration MySQL")
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal("cannot access consumer integration pool")
	}
	pool.SetMaxOpenConns(10)
	t.Cleanup(func() { _ = pool.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	connection, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal("cannot reserve consumer integration lock")
	}
	var acquired int
	if connection.QueryRowContext(ctx, "SELECT GET_LOCK('go072_integration',30)").Scan(&acquired) != nil || acquired != 1 {
		_ = connection.Close()
		t.Fatal("GO072 integration fixture window unavailable")
	}
	t.Cleanup(func() {
		releaseCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var released int
		if connection.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK('go072_integration')").Scan(&released) != nil || released != 1 {
			t.Error("cannot release consumer integration lock")
		}
		_ = connection.Close()
	})
	return db, cfg
}

type consumerFixture struct {
	db              *gorm.DB
	cfg             config.Config
	runtime         *communityprojection.Runtime
	base            uint64
	user, target    string
	note, shop      string
	events, topics  []string
	groups          []string
	client          *kafka.Client
	transport       *kafka.Transport
	seeded, hasNote bool
}

func newConsumerFixture(t *testing.T, db *gorm.DB, cfg config.Config) *consumerFixture {
	t.Helper()
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("cannot allocate consumer fixture")
	}
	base := uint64(9_400_000_000_000_000) + binary.LittleEndian.Uint64(entropy[:])%1_000_000_000_000
	f := &consumerFixture{db: db, cfg: cfg, base: base, runtime: communityprojection.NewRuntime(communityprojection.New(db))}
	f.user, f.target = strconv.FormatUint(base+1, 10), strconv.FormatUint(base+2, 10)
	f.note, f.shop = strconv.FormatUint(base+20, 10), strconv.FormatUint(base, 10)
	f.transport = &kafka.Transport{DialTimeout: 2 * time.Second, IdleTimeout: time.Second}
	f.client = &kafka.Client{Addr: kafka.TCP(cfg.Kafka.Brokers...), Timeout: 5 * time.Second, Transport: f.transport}
	var existing int64
	if db.Raw(`SELECT (SELECT COUNT(*) FROM tb_user WHERE id IN (?,?)) +
	 (SELECT COUNT(*) FROM tb_merchant WHERE id=?) +
	 (SELECT COUNT(*) FROM tb_shop WHERE id=?) + (SELECT COUNT(*) FROM tb_blog WHERE id=?) +
	 (SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=?) +
	 (SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id=?)`, f.user, f.target, f.shop, f.shop, f.note, f.user, f.target, f.note).Scan(&existing).Error != nil || existing != 0 {
		t.Fatal("cannot establish exact consumer fixture ownership")
	}
	t.Cleanup(func() { f.cleanup(t) })
	err := db.Transaction(func(tx *gorm.DB) error {
		for index, id := range []string{f.user, f.target} {
			phone := fmt.Sprintf("136%08d", (base+uint64(index))%100_000_000)
			if err := tx.Exec("INSERT INTO tb_user(id,phone,nick_name) VALUES(?,?,'owned GO072 consumer')", id, phone).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("INSERT INTO tb_merchant(id,name) VALUES(?,'owned GO072 consumer merchant')", f.shop).Error; err != nil {
			return err
		}
		return tx.Exec("INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(?,?,'owned GO072 consumer shop',1,'owned fixture',120,30)", f.shop, f.shop).Error
	})
	if err != nil {
		t.Fatal("cannot seed exact consumer fixture")
	}
	f.seeded = true
	return f
}

func (f *consumerFixture) topic(t *testing.T) string {
	t.Helper()
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("cannot allocate private topic")
	}
	topic := "community.go072.it." + hex.EncodeToString(entropy[:])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := (&kafka.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", f.cfg.Kafka.Brokers[0])
	if err != nil {
		t.Fatal("isolated Kafka unavailable")
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if connection.CreateTopics(kafka.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}) != nil {
		t.Fatal("cannot establish exclusive private Kafka topic")
	}
	f.topics = append(f.topics, topic)
	// CreateTopics acknowledges the controller update, not that this partition
	// already has a usable leader. Establish readiness before the single write;
	// retrying an uncertain write could manufacture extra offsets in this test.
	readyCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	for readyCtx.Err() == nil {
		leader, err := (&kafka.Dialer{Timeout: 2 * time.Second}).DialLeader(readyCtx, "tcp", f.cfg.Kafka.Brokers[0], topic, 0)
		if err == nil {
			_ = leader.SetDeadline(time.Now().Add(2 * time.Second))
			last, readErr := leader.ReadLastOffset()
			_ = leader.Close()
			if readErr == nil && last == 0 {
				return topic
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("private partition leader did not become ready before writing")
	return topic
}

func (f *consumerFixture) worker(t *testing.T, topic, projection, group string, processor Processor) *Worker {
	t.Helper()
	worker, err := newWorkerWithGroup(f.cfg.Kafka.Brokers, topic, projection, group, processor)
	if err != nil {
		t.Fatal("cannot construct private Kafka consumer")
	}
	f.groups = append(f.groups, group)
	t.Cleanup(func() {
		if worker.Close() != nil {
			t.Error("private consumer did not close")
		}
	})
	return worker
}

func (f *consumerFixture) follow(version int, active bool) kafka.Message {
	id := "FOLLOW:" + f.user + ":" + f.target + ":" + strconv.Itoa(version)
	f.events = append(f.events, id)
	return encodedEvent(id, "FOLLOW_CHANGED", "FOLLOW", f.target, version, map[string]any{"userId": f.user, "followUserId": f.target, "active": active, "version": version})
}
func (f *consumerFixture) published(version int) kafka.Message {
	id := "NOTE:" + f.note + ":" + strconv.Itoa(version) + ":PUBLISHED"
	f.events = append(f.events, id)
	return encodedEvent(id, "NOTE_PUBLISHED", "BLOG", f.note, version, map[string]any{"noteId": f.note, "authorId": f.target, "publishVersion": version})
}
func encodedEvent(id, eventType, aggregateType, aggregateID string, version int, payload any) kafka.Message {
	body, _ := json.Marshal(map[string]any{"eventId": id, "eventType": eventType, "aggregateType": aggregateType, "aggregateId": aggregateID, "version": version, "payload": payload})
	return kafka.Message{Key: []byte(aggregateID), Value: body, Headers: []kafka.Header{{Key: "eventId", Value: []byte(id)}, {Key: "eventType", Value: []byte(eventType)}, {Key: "version", Value: []byte(strconv.Itoa(version))}}}
}
func (f *consumerFixture) write(t *testing.T, topic string, messages ...kafka.Message) {
	t.Helper()
	// The default global transport caches an all-topic metadata snapshot for
	// six seconds, including negative lookups for later freshly created topics.
	// A private transport keeps this fixture independent of earlier test topics.
	transport := &kafka.Transport{DialTimeout: 2 * time.Second, MetadataTopics: []string{topic}}
	defer transport.CloseIdleConnections()
	writer := &kafka.Writer{Addr: kafka.TCP(f.cfg.Kafka.Brokers...), Topic: topic, RequiredAcks: kafka.RequireAll, BatchTimeout: time.Millisecond,
		WriteTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, MaxAttempts: 1, Transport: transport}
	defer func() { _ = writer.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := writer.WriteMessages(ctx, messages...); err != nil {
		t.Fatalf("cannot publish exact private source messages: kind=%T unknownTopic=%t notLeader=%t deadline=%t", err,
			errors.Is(err, kafka.UnknownTopicOrPartition), errors.Is(err, kafka.NotLeaderForPartition), errors.Is(err, context.DeadlineExceeded))
	}
}
func (f *consumerFixture) fact(t *testing.T, version int) {
	t.Helper()
	if f.db.Exec(`INSERT INTO tb_blog(id,shop_id,user_id,title,images,content,status,publish_version)
	 VALUES(?,?,?,'owned consumer note','','owned fixture','PUBLISHED',?)`, f.note, f.shop, f.target, version).Error != nil {
		t.Fatal("cannot create exact missing publication fact")
	}
	f.hasNote = true
}
func (f *consumerFixture) scalar(t *testing.T, query string, want int64, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var value int64
	if f.db.WithContext(ctx).Raw(query, args...).Scan(&value).Error != nil || value != want {
		t.Fatalf("consumer DB invariant wanted %d, got %d", want, value)
	}
}
func (f *consumerFixture) offset(t *testing.T, topic, group string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := f.client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: group, Topics: map[string][]int{topic: {0}}})
	if err != nil || response.Error != nil || len(response.Topics[topic]) != 1 || response.Topics[topic][0].Error != nil {
		t.Fatal("cannot obtain authoritative broker group offset")
	}
	return response.Topics[topic][0].CommittedOffset
}
func (f *consumerFixture) drain(t *testing.T, worker *Worker, topic, group string, expected int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		processed, err := worker.Poll(ctx)
		if !processed {
			if err != nil {
				t.Fatal("private consumer could not fetch")
			}
			continue
		}
		if err == nil && f.offset(t, topic, group) == expected {
			return
		}
	}
	t.Fatal("private consumer did not reach its broker-confirmed offset")
}
func (f *consumerFixture) cleanup(t *testing.T) {
	t.Helper()
	defer f.transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if f.seeded {
		err := f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if len(f.events) > 0 {
				if err := tx.Exec("DELETE FROM tb_community_projection_event WHERE projection IN ('follow-v1','feed-v1') AND event_id IN ?", f.events).Error; err != nil {
					return err
				}
			}
			if len(f.topics) > 0 {
				if err := tx.Exec("DELETE FROM tb_community_consumer_failure WHERE topic IN ?", f.topics).Error; err != nil {
					return err
				}
			}
			for _, deletion := range []struct {
				query string
				args  []any
			}{
				{"DELETE FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", []any{f.user, f.target}},
				{"DELETE FROM tb_community_note_projection WHERE note_id=? AND author_id=?", []any{f.note, f.target}},
				{"DELETE FROM tb_blog WHERE id=? AND user_id=? AND shop_id=?", []any{f.note, f.target, f.shop}},
				{"DELETE FROM tb_shop WHERE id=?", []any{f.shop}},
				{"DELETE FROM tb_merchant WHERE id=?", []any{f.shop}},
				{"DELETE FROM tb_user WHERE id IN (?,?)", []any{f.user, f.target}},
			} {
				if err := tx.Exec(deletion.query, deletion.args...).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Error("cannot clean exact consumer SQL fixture")
		}
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic IN ?", 0, f.topics)
		if len(f.events) > 0 {
			f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE event_id IN ?", 0, f.events)
		}
	}
	if len(f.groups) > 0 {
		response, err := f.client.DeleteGroups(ctx, &kafka.DeleteGroupsRequest{GroupIDs: f.groups})
		if err != nil {
			t.Error("cannot delete exact private consumer groups")
		} else {
			for _, failure := range response.Errors {
				if failure != nil && !errors.Is(failure, kafka.GroupIdNotFound) {
					t.Error("private consumer group cleanup failed")
				}
			}
		}
	}
	if len(f.topics) > 0 {
		response, err := f.client.DeleteTopics(ctx, &kafka.DeleteTopicsRequest{Topics: f.topics})
		if err != nil {
			t.Error("cannot delete exact private consumer topics")
		} else {
			for _, failure := range response.Errors {
				if failure != nil {
					t.Error("private topic cleanup failed")
				}
			}
		}
	}
	t.Log("cleanup=exact SQL fixture/private Kafka topic/private groups; shared community.events untouched")
}

func TestIntegrationCommunityConsumer(t *testing.T) {
	db, cfg := consumerDatabase(t)
	t.Run("follow_duplicate_and_reverse_versions_commit_broker_offset_without_reverting_v3", func(t *testing.T) {
		f := newConsumerFixture(t, db, cfg)
		topic := f.topic(t)
		v3, v2, v1 := f.follow(3, true), f.follow(2, false), f.follow(1, true)
		f.write(t, topic, v3, v2, v1, v3)
		group := topic + ".follow"
		worker := f.worker(t, topic, communityprojection.Follow, group, f.runtime)
		f.drain(t, worker, topic, group, 4)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND active=1 AND version=3", 1, f.user, f.target)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='follow-v1' AND event_id IN ? AND state='APPLIED'", 3, f.events)
		t.Logf("topic=%s group=%s sentVersions=3,2,1,3 durableVersion=3 active=true receipts=3 brokerCommittedNextOffset=4", topic, group)
	})
	t.Run("feed_duplicate_and_reverse_publication_versions_keep_one_current_catalog_row", func(t *testing.T) {
		f := newConsumerFixture(t, db, cfg)
		f.fact(t, 2)
		topic := f.topic(t)
		v2, v1 := f.published(2), f.published(1)
		f.write(t, topic, v2, v1, v2)
		group := topic + ".feed"
		worker := f.worker(t, topic, communityprojection.Feed, group, f.runtime)
		f.drain(t, worker, topic, group, 3)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id=? AND author_id=? AND version=2", 1, f.note, f.target)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='feed-v1' AND event_id IN ?", 2, f.events)
		t.Logf("topic=%s group=%s sentVersions=2,1,2 durableVersion=2 receipts=2 brokerCommittedNextOffset=3", topic, group)
	})
	t.Run("committed_effect_before_offset_context_cancel_restarts_same_group_and_replays_once", func(t *testing.T) {
		f := newConsumerFixture(t, db, cfg)
		topic := f.topic(t)
		message := f.follow(1, true)
		f.write(t, topic, message)
		group := topic + ".window"
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		processor := processorFuncs{apply: func(ctx context.Context, projection string, source Message) error {
			if err := f.runtime.Apply(ctx, projection, source); err != nil {
				return err
			}
			cancel() // Exact post-commit/pre-offset window, not an OS process kill.
			return nil
		}, park: f.runtime.Park}
		first := f.worker(t, topic, communityprojection.Follow, group, processor)
		for first.pending == nil && ctx.Err() == nil {
			_, _ = first.Poll(ctx)
		}
		if ctx.Err() == nil || !first.durable {
			t.Fatal("did not hit durable-effect-before-offset cancellation")
		}
		if first.Close() != nil {
			t.Fatal("old group member did not leave")
		}
		if offset := f.offset(t, topic, group); offset != -1 {
			t.Fatal("cancelled instance unexpectedly committed its source")
		}
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='follow-v1' AND event_id=?", 1, f.events[0])
		t.Logf("time=%s window=DB_COMMIT_BEFORE_OFFSET topic=%s group=%s sourceOffset=0 brokerCommittedNextOffset=-1 durableReceipts=1 contextCancelled=true", time.Now().UTC().Format(time.RFC3339Nano), topic, group)
		second := f.worker(t, topic, communityprojection.Follow, group, f.runtime)
		f.drain(t, second, topic, group, 1)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='follow-v1' AND event_id=?", 1, f.events[0])
		f.scalar(t, "SELECT version FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", 1, f.user, f.target)
		t.Logf("time=%s recovery=newReaderSameGroup replayedSourceOffset=0 brokerCommittedNextOffset=1 durableReceipts=1 version=1", time.Now().UTC().Format(time.RFC3339Nano))
	})
	t.Run("closed_db_pool_cannot_apply_park_or_commit_and_healthy_recovery_parks_then_replays", func(t *testing.T) {
		f := newConsumerFixture(t, db, cfg)
		topic := f.topic(t)
		f.write(t, topic, f.follow(1, true))
		closed, err := gorm.Open(gormmysql.New(gormmysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal("cannot arrange separate closed MySQL pool")
		}
		pool, err := closed.DB()
		if err != nil || pool.Close() != nil {
			t.Fatal("cannot close the independent consumer DB pool")
		}
		group := topic + ".db"
		worker := f.worker(t, topic, communityprojection.Follow, group, communityprojection.NewRuntime(communityprojection.New(closed)))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for worker.pending == nil && ctx.Err() == nil {
			_, _ = worker.Poll(ctx)
		}
		for range 6 {
			if _, err := worker.Poll(ctx); err == nil {
				t.Fatal("closed pool cannot prove a durable Apply or Park")
			}
		}
		if worker.pending == nil || worker.attempts != 3 || worker.durable || f.offset(t, topic, group) != -1 {
			t.Fatal("DB failure lost the uncommitted source after bounded apply attempts")
		}
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=?", 0, topic)
		worker.processor = f.runtime
		f.drain(t, worker, topic, group, 1)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=? AND state='PARKED'", 1, topic)
		if f.runtime.Replay(context.Background(), communityprojection.Follow, topic, 0, 0) != nil {
			t.Fatal("healthy storage did not replay the durable parked source")
		}
		f.scalar(t, "SELECT version FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", 1, f.user, f.target)
		t.Logf("topic=%s group=%s fault=closedIndependentSQLPool attempts=3 parkFailuresOverThreshold=true offsetBefore=-1 offsetAfter=1 replay=RESOLVED physicalDatabaseStop=false", topic, group)
	})
	t.Run("oversized_header_poison_parks_and_one_blocked_group_does_not_stop_the_other", func(t *testing.T) {
		f := newConsumerFixture(t, db, cfg)
		topic := f.topic(t)
		poison, normal := f.follow(1, true), f.follow(2, false)
		for index := 0; index < 31; index++ {
			poison.Headers = append(poison.Headers, kafka.Header{Key: fmt.Sprintf("owned-%d", index), Value: []byte("extra")})
		}
		f.write(t, topic, poison, normal)
		blockedGroup, healthyGroup := topic+".feed", topic+".follow"
		blocked := f.worker(t, topic, communityprojection.Feed, blockedGroup, processorFuncs{apply: f.runtime.Apply,
			park: func(context.Context, string, Message, string) error {
				return errors.New("controlled per-group parking unavailable")
			}})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for blocked.pending == nil && ctx.Err() == nil {
			_, _ = blocked.Poll(ctx)
		}
		for range 3 {
			if _, err := blocked.Poll(ctx); err == nil {
				t.Fatal("blocked group advanced without durable parking")
			}
		}
		if f.offset(t, topic, blockedGroup) != -1 {
			t.Fatal("poison was committed without a persistent parking row")
		}
		healthy := f.worker(t, topic, communityprojection.Follow, healthyGroup, f.runtime)
		f.drain(t, healthy, topic, healthyGroup, 2)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND version=2 AND active=0", 1, f.user, f.target)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection='follow-v1' AND topic=? AND source_offset=0 AND state='PARKED' AND JSON_LENGTH(message_headers)=34", 1, topic)
		f.scalar(t, `SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection='follow-v1' AND topic=?
	 AND partition_id=0 AND source_offset=0 AND message_key=? AND message_body=? AND OCTET_LENGTH(message_hash)=32 AND reason='apply_failed'`, 1, topic, poison.Key, poison.Value)
		if f.offset(t, topic, blockedGroup) != -1 {
			t.Fatal("another group's commit leaked into the blocked group's offsets")
		}
		blocked.processor = f.runtime
		f.drain(t, blocked, topic, blockedGroup, 2)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=? AND state='PARKED'", 2, topic)
		t.Logf("topic=%s poisonSourceOffset=0 originalHeaders=34 healthyGroupNextOffset=2 blockedGroupNextOffset=-1 recoveredGroupNextOffset=2 durablePoisonRows=2", topic)
	})
	t.Run("missing_publication_fact_parks_then_controlled_replay_applies_and_resolves_atomically", func(t *testing.T) {
		f := newConsumerFixture(t, db, cfg)
		topic := f.topic(t)
		f.write(t, topic, f.published(1))
		group := topic + ".replay"
		worker := f.worker(t, topic, communityprojection.Feed, group, f.runtime)
		f.drain(t, worker, topic, group, 1)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id=?", 0, f.note)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='feed-v1' AND event_id=?", 0, f.events[0])
		if !errors.Is(f.runtime.Replay(context.Background(), communityprojection.Feed, topic, 0, 0), communityprojection.ErrFactUnavailable) {
			t.Fatal("replay without a business fact must preserve its parked source")
		}
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=? AND state='PARKED'", 1, topic)
		f.fact(t, 1)
		for range 2 {
			if f.runtime.Replay(context.Background(), communityprojection.Feed, topic, 0, 0) != nil {
				t.Fatal("replay after restoring only the owned fact did not resolve")
			}
		}
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id=? AND version=1", 1, f.note)
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='feed-v1' AND event_id=?", 1, f.events[0])
		f.scalar(t, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=? AND state='RESOLVED' AND resolved_at IS NOT NULL", 1, topic)
		t.Logf("topic=%s group=%s brokerCommittedNextOffset=1 beforeFact=parked/noEffect afterFact=RESOLVED/catalogVersion1 receipts=1 duplicateReplay=idem", topic, group)
	})
}
