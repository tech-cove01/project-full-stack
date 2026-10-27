package communityrelay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// The suite owns one topic and the exact outbox rows it inserts; it never flushes
// or resets anything and never writes to the shared community.events topic.
func TestIntegrationCommunityRelay(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	if len(cfg.Kafka.Brokers) == 0 {
		t.Skip("KAFKA_BROKERS is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("dependency cleanup failed")
		}
	})
	suffix := randomSuffix(t)
	topic := "community.events.it." + suffix
	first := "IT:" + suffix + ":1"
	second := "IT:" + suffix + ":2"
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.ORM.WithContext(cleanupCtx).Exec(
			"DELETE FROM tb_community_outbox WHERE event_id IN (?,?)", first, second).Error
	})
	for _, eventID := range []string{first, second} {
		if err = resources.ORM.WithContext(ctx).Exec(`INSERT INTO tb_community_outbox
			(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload)
			VALUES (?,?,?,?,?,?)`, eventID, "FOLLOW", "42", 1, "FOLLOW_CHANGED",
			`{"userId":"1","followUserId":"42","active":true,"version":1}`).Error; err != nil {
			t.Fatal("outbox fixture insert failed")
		}
	}
	ensureTopic(t, cfg.Kafka.Brokers[0], topic)
	worker := NewWorker(resources.ORM, cfg.Kafka.Brokers, topic)
	defer func() { _ = worker.Close() }()
	if processed, err := worker.Poll(ctx); err != nil || !processed {
		t.Fatalf("the relay must claim the due event: processed=%v err=%v", processed, err)
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: cfg.Kafka.Brokers, Topic: topic, Partition: 0,
		MinBytes: 1, MaxBytes: 10 << 20,
	})
	defer func() { _ = reader.Close() }()
	readCtx, stopRead := context.WithTimeout(ctx, 10*time.Second)
	defer stopRead()
	message, err := reader.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("the published event was not readable: %v", err)
	}
	var delivered envelope
	if err = json.Unmarshal(message.Value, &delivered); err != nil {
		t.Fatalf("envelope is not JSON: %v", err)
	}
	if delivered.EventID != first || delivered.EventType != "FOLLOW_CHANGED" ||
		delivered.AggregateID != "42" || delivered.Version != 1 ||
		string(delivered.Payload) == "" {
		t.Fatalf("unexpected envelope: %+v", delivered)
	}
	if got := kafkaHeader(message, "eventId"); got != first {
		t.Fatalf("the event id header must match, got %q", got)
	}
	if state := deliveryState(t, resources, first); state != "DONE" {
		t.Fatalf("an acknowledged event must be DONE, got %s", state)
	}
	// Poll again: the done row must not be delivered twice.
	if processed, err := worker.Poll(ctx); err != nil || !processed {
		t.Fatalf("the second due event must be claimed: processed=%v err=%v", processed, err)
	}
	if state := deliveryState(t, resources, second); state != "DONE" {
		t.Fatalf("the second event must be DONE, got %s", state)
	}
	if processed, err := worker.Poll(ctx); err != nil || processed {
		t.Fatalf("an empty queue must not report work: processed=%v err=%v", processed, err)
	}

	// An unreachable broker must keep the row retryable instead of losing it.
	if err = resources.ORM.WithContext(ctx).Exec(`INSERT INTO tb_community_outbox
		(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload)
		VALUES (?,?,?,?,?,?)`, "IT:"+suffix+":3", "FOLLOW", "42", 2, "FOLLOW_CHANGED", `{}`).Error; err != nil {
		t.Fatal("failing fixture insert failed")
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.ORM.WithContext(cleanupCtx).Exec(
			"DELETE FROM tb_community_outbox WHERE event_id=?", "IT:"+suffix+":3").Error
	})
	broken := NewWorker(resources.ORM, []string{"127.0.0.1:1"}, topic)
	defer func() { _ = broken.Close() }()
	if processed, err := broken.Poll(ctx); err != nil || !processed {
		t.Fatalf("a failing delivery must be reported, not lost: processed=%v err=%v", processed, err)
	}
	var retry struct {
		Status     string
		Attempts   int
		FutureNext bool
	}
	if err = resources.ORM.WithContext(ctx).Raw(`SELECT delivery_status AS status, attempts,
		next_attempt_at > NOW(3) AS future_next FROM tb_community_outbox WHERE event_id=?`,
		"IT:"+suffix+":3").Scan(&retry).Error; err != nil {
		t.Fatal(err)
	}
	if retry.Status != "PENDING" || retry.Attempts != 1 || !retry.FutureNext {
		t.Fatalf("a failed delivery must be retried later: %+v", retry)
	}
}

// ensureTopic creates the test topic explicitly so a slow auto-create cannot be
// mistaken for a lost message.
func ensureTopic(t *testing.T, broker, topic string) {
	t.Helper()
	conn, err := kafka.Dial("tcp", broker)
	if err != nil {
		t.Fatal("kafka broker is unreachable")
	}
	defer func() { _ = conn.Close() }()
	err = conn.CreateTopics(kafka.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("topic creation failed: %v", err)
	}
}

func deliveryState(t *testing.T, resources *database.Resources, eventID string) string {
	t.Helper()
	var state string
	if err := resources.ORM.Raw(`SELECT delivery_status FROM tb_community_outbox
		WHERE event_id=?`, eventID).Scan(&state).Error; err != nil {
		t.Fatal(err)
	}
	return state
}

func kafkaHeader(message kafka.Message, key string) string {
	for _, header := range message.Headers {
		if header.Key == key {
			return string(header.Value)
		}
	}
	return ""
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var entropy [4]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	return strings.ToLower(hex.EncodeToString(entropy[:])) + strconv.Itoa(time.Now().Nanosecond()%1000)
}
