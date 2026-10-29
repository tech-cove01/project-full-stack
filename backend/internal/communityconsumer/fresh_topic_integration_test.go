package communityconsumer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// This test needs a dedicated disposable broker: it intentionally begins with
// no such topic and does not delete topics or reset offsets.
func TestIntegrationFreshTopicStartsAllGroupsAndPreservesOffsets(t *testing.T) {
	if os.Getenv("GO073_KAFKA_FRESH") != "1" {
		t.Skip("requires dedicated GO073 broker")
	}
	if os.Getenv("KAFKA_BROKERS") != "127.0.0.1:9122" {
		t.Fatal("refusing non-dedicated broker")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	topic := "community.go073.fresh." + hex.EncodeToString(nonce[:])
	groups := []string{topic + ".follow", topic + ".feed", topic + ".search"}
	t.Logf("owned topic=%s groups=%v; no offsets reset or topics deleted", topic, groups)
	transport := &kafka.Transport{DialTimeout: 2 * time.Second, IdleTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &kafka.Client{Addr: kafka.TCP("127.0.0.1:9122"), Timeout: 5 * time.Second, Transport: transport}
	metadata := func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		freshTransport := &kafka.Transport{DialTimeout: 2 * time.Second, IdleTimeout: time.Second}
		defer freshTransport.CloseIdleConnections()
		freshClient := &kafka.Client{Addr: kafka.TCP("127.0.0.1:9122"), Timeout: 5 * time.Second, Transport: freshTransport}
		r, err := freshClient.Metadata(ctx, &kafka.MetadataRequest{})
		if err != nil {
			t.Fatalf("list metadata: %v", err)
		}
		for _, candidate := range r.Topics {
			if candidate.Name == topic {
				return true
			}
		}
		return false
	}
	if metadata() {
		t.Fatal("generated topic already exists")
	}
	received := make(chan struct {
		group  string
		offset int64
	}, 12)
	var cancelAll []context.CancelFunc
	var dones []<-chan struct{}
	start := func(i int) {
		worker, err := newWorkerWithGroup([]string{"127.0.0.1:9122"}, topic, []string{"follow-v1", "feed-v1", "search-v1"}[i], groups[i], processorFuncs{
			apply: func(_ context.Context, _ string, m Message) error {
				received <- struct {
					group  string
					offset int64
				}{groups[i], m.Offset}
				return nil
			},
			park: func(context.Context, string, Message, string) error { return errors.New("unexpected park") },
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		cancelAll = append(cancelAll, cancel)
		dones = append(dones, done)
		go func() { defer close(done); worker.Run(ctx) }()
	}
	defer func() {
		for _, cancel := range cancelAll {
			cancel()
		}
		for _, done := range dones {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("worker failed to stop")
			}
		}
	}()
	for i := range groups {
		start(i)
	}
	time.Sleep(4 * time.Second)
	if metadata() {
		t.Fatal("consumers created an absent topic before the producer")
	}
	writer := &kafka.Writer{Addr: kafka.TCP("127.0.0.1:9122"), Topic: topic, AllowAutoTopicCreation: true, RequiredAcks: kafka.RequireAll, BatchTimeout: time.Millisecond, WriteTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		err := writer.WriteMessages(ctx, kafka.Message{Key: []byte("fixture"), Value: []byte("first")})
		if err == nil {
			break
		}
		// Metadata may report UNKNOWN_TOPIC while auto-creation elects a leader;
		// this is not an acknowledged or uncertain publish. Do not retry any
		// other error that could represent a partially completed write.
		if !errors.Is(err, kafka.UnknownTopicOrPartition) || ctx.Err() != nil {
			t.Fatalf("producer could not create topic and write: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	await := func(offset int64) {
		seen := map[string]bool{}
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for len(seen) < 3 {
			select {
			case msg := <-received:
				if msg.offset != offset || seen[msg.group] {
					t.Fatalf("unexpected delivery: %+v", msg)
				}
				seen[msg.group] = true
			case <-timer.C:
				t.Fatalf("offset %d reached only %v", offset, seen)
			}
		}
	}
	await(0)
	// Apply returns just before CommitMessages: wait for authoritative offsets.
	for _, group := range groups {
		for deadline := time.Now().Add(15 * time.Second); ; {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			response, err := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: group, Topics: map[string][]int{topic: {0}}})
			cancel()
			if err == nil && response.Error == nil && len(response.Topics[topic]) == 1 && response.Topics[topic][0].CommittedOffset == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("group %s never committed first record", group)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	for _, cancel := range cancelAll {
		cancel()
	}
	for _, done := range dones {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("first workers failed to stop")
		}
	}
	cancelAll = nil
	dones = nil
	for i := range groups {
		start(i)
	}
	if err := writer.WriteMessages(ctx, kafka.Message{Key: []byte("fixture"), Value: []byte("second")}); err != nil {
		t.Fatal(err)
	}
	await(1)
}

func TestIntegrationUnavailableBrokerCancelsReadiness(t *testing.T) {
	if os.Getenv("GO073_KAFKA_FRESH") != "1" {
		t.Skip("requires dedicated GO073 broker")
	}
	worker, err := NewWorker([]string{"127.0.0.1:1"}, "community.go073.unavailable", "feed-v1", processorFuncs{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("unavailable broker blocked shutdown")
	}
	if worker.reader != nil {
		t.Fatal("unavailable broker started a group reader")
	}
}
