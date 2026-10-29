// Package communityconsumer commits Kafka offsets only after a durable
// projection effect or a durable, replayable failure record.
package communityconsumer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/cityloop/fullstack-go/internal/communityprojection"
)

type Message = communityprojection.Message

// Nil from Apply or Park proves a committed DB outcome. Unknown/failed writes
// must return an error, including commit errors. Park preserves the original
// source and body for controlled replay, rather than an in-memory dead letter.
type Processor interface {
	Apply(context.Context, string, Message) error
	Park(context.Context, string, Message, string) error
}

type reader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

type Worker struct {
	reader       reader
	readerMu     sync.Mutex
	readerConfig *kafka.ReaderConfig
	closed       bool
	processor    Processor
	projection   string
	// A consumer must not join its group before the topic has partitions. In
	// kafka-go 0.4.47 an empty initial assignment can race its partition watcher
	// and stay empty after automatic topic creation.
	topicReady    func(context.Context) bool
	closeMetadata func()
	ready         bool
	pending       *kafka.Message
	attempts      int
	durable       bool
	closeOnce     sync.Once
	closeError    error

	readTimeout, applyTimeout, commitTimeout, retryDelay time.Duration
}

// NewWorker uses stable independent groups. Each projection sees the complete
// topic; introducing more projections requires an explicit reviewed contract.
func NewWorker(brokers []string, topic, projection string, processor Processor) (*Worker, error) {
	group := "community." + projection
	return newWorkerWithGroup(brokers, topic, projection, group, processor)
}

// Only tests in this package choose private topics and groups. Production
// callers cannot accidentally share offsets between independent projections.
func newWorkerWithGroup(brokers []string, topic, projection, group string, processor Processor) (*Worker, error) {
	if projection != "follow-v1" && projection != "feed-v1" && projection != "search-v1" {
		return nil, errors.New("unsupported community projection")
	}
	if processor == nil || len(brokers) == 0 || strings.TrimSpace(topic) == "" || strings.TrimSpace(group) == "" {
		return nil, errors.New("invalid community consumer configuration")
	}
	for _, broker := range brokers {
		if strings.TrimSpace(broker) == "" {
			return nil, errors.New("invalid community consumer broker")
		}
	}
	configuration := kafka.ReaderConfig{
		Brokers: append([]string(nil), brokers...), Topic: topic, GroupID: group,
		MinBytes: 1, MaxBytes: 10 << 20, MaxWait: time.Second,
		QueueCapacity: 1, ReadBatchTimeout: time.Second,
		StartOffset: kafka.FirstOffset, CommitInterval: 0,
		SessionTimeout: 10 * time.Second, HeartbeatInterval: 3 * time.Second,
		RebalanceTimeout:      10 * time.Second,
		WatchPartitionChanges: true, PartitionWatchInterval: 2 * time.Second,
		Dialer: &kafka.Dialer{Timeout: 2 * time.Second, DualStack: true},
	}
	if configuration.Validate() != nil {
		return nil, errors.New("invalid community consumer configuration")
	}
	// NewReader immediately starts the consumer-group goroutine.
	worker := workerWithReader(nil, projection, processor)
	worker.readerConfig = &configuration
	transport := &kafka.Transport{DialTimeout: 2 * time.Second, IdleTimeout: time.Second, MetadataTTL: time.Second}
	client := &kafka.Client{Addr: kafka.TCP(brokers...), Timeout: 2 * time.Second, Transport: transport}
	worker.topicReady = func(ctx context.Context) bool {
		// Metadata API explicitly disables broker-side auto-creation. The relay
		// remains responsible for creating the configured topic on first write.
		response, err := client.Metadata(ctx, &kafka.MetadataRequest{Topics: []string{topic}})
		if err != nil || len(response.Topics) != 1 || response.Topics[0].Name != topic || response.Topics[0].Error != nil || len(response.Topics[0].Partitions) == 0 {
			return false
		}
		for _, partition := range response.Topics[0].Partitions {
			if partition.Error != nil || partition.Leader.Host == "" {
				return false
			}
		}
		return true
	}
	worker.closeMetadata = transport.CloseIdleConnections
	return worker, nil
}

func workerWithReader(source reader, projection string, processor Processor) *Worker {
	return &Worker{reader: source, projection: projection, processor: processor,
		readTimeout: time.Second, applyTimeout: 5 * time.Second,
		commitTimeout: 5 * time.Second, retryDelay: 200 * time.Millisecond}
}

func (worker *Worker) Close() error {
	worker.closeOnce.Do(func() {
		worker.readerMu.Lock()
		worker.closed = true
		source := worker.reader
		worker.readerMu.Unlock()
		if source != nil {
			worker.closeError = source.Close()
		}
		if worker.closeMetadata != nil {
			worker.closeMetadata()
		}
	})
	return worker.closeError
}

// Run owns reader shutdown. The app must await Run before closing the DB pool.
// Fetch cancellation and synchronous commits inherit process cancellation;
// Close joins the Kafka reader's own goroutines instead of abandoning them.
func (worker *Worker) Run(ctx context.Context) {
	defer func() { _ = worker.Close() }()
	for ctx.Err() == nil {
		processed, err := worker.Poll(ctx)
		if ctx.Err() != nil || errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			// Driver errors can include payload fragments; never print them.
			slog.Warn("community projection will retry", "projection", worker.projection)
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(worker.retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Poll makes one bounded attempt at the current message. Poll/Run have one
// goroutine owner. A failed Apply, Park or Commit keeps the same message; no
// later offset is fetched by this worker until it has durable proof AND a
// successful synchronous commit. Kafka itself may prefetch without committing.
func (worker *Worker) Poll(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	worker.readerMu.Lock()
	closed := worker.closed
	worker.readerMu.Unlock()
	if closed {
		return false, io.EOF
	}
	if !worker.ready && worker.topicReady != nil {
		readyCtx, cancel := context.WithTimeout(ctx, worker.readTimeout)
		ready := worker.topicReady(readyCtx)
		cancel()
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !ready {
			return false, nil
		}
		worker.readerMu.Lock()
		if worker.closed {
			worker.readerMu.Unlock()
			return false, io.EOF
		}
		worker.reader = kafka.NewReader(*worker.readerConfig)
		worker.readerMu.Unlock()
		worker.ready = true
	}
	if worker.pending == nil {
		readCtx, cancel := context.WithTimeout(ctx, worker.readTimeout)
		message, err := worker.reader.FetchMessage(readCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return false, nil
			}
			if errors.Is(err, io.EOF) {
				return false, io.EOF
			}
			return false, errors.New("community message fetch unavailable")
		}
		worker.pending = &message
	}
	if !worker.durable {
		if worker.attempts < 3 {
			worker.attempts++
			applyCtx, cancel := context.WithTimeout(ctx, worker.applyTimeout)
			err := worker.processor.Apply(applyCtx, worker.projection, sourceMessage(*worker.pending))
			cancel()
			if err == nil {
				worker.durable = true
			} else {
				if ctx.Err() != nil {
					return true, ctx.Err()
				}
				if worker.attempts < 3 {
					return true, errors.New("community projection apply not durable")
				}
			}
		}
		if !worker.durable {
			parkCtx, cancel := context.WithTimeout(ctx, worker.applyTimeout)
			err := worker.processor.Park(parkCtx, worker.projection, sourceMessage(*worker.pending), "apply_failed")
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return true, ctx.Err()
				}
				return true, errors.New("community message parking not durable")
			}
			worker.durable = true
		}
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	commitCtx, cancel := context.WithTimeout(ctx, worker.commitTimeout)
	err := worker.reader.CommitMessages(commitCtx, *worker.pending)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		return true, errors.New("community offset commit not confirmed")
	}
	worker.pending, worker.attempts, worker.durable = nil, 0, false
	return true, nil
}

func sourceMessage(source kafka.Message) Message {
	message := Message{Topic: source.Topic, Partition: source.Partition, Offset: source.Offset,
		Key: append([]byte(nil), source.Key...), Body: append([]byte(nil), source.Value...)}
	for _, header := range source.Headers {
		message.Headers = append(message.Headers, communityprojection.Header{Key: header.Key, Value: append([]byte(nil), header.Value...)})
	}
	return message
}
