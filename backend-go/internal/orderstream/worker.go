// Package orderstream delivers Redis reservations to a durable MySQL outcome.
package orderstream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cityloop/fullstack-go/internal/order"
)

type Processor interface {
	Create(context.Context, string, string, string) (order.Outcome, error)
}

// Reviewer persists an accepted order whose database outcome is still unknown.
// Its record must exist before the message may be acknowledged.
type Reviewer interface {
	Record(ctx context.Context, orderID, userID, voucherID, messageID string, attempts int, cause error) error
	Resolve(ctx context.Context, orderID string) error
}

// reviewThreshold is the attempt count after which an unresolved message is
// parked in tb_order_review (and only then acknowledged).
const reviewThreshold = 5

type retryState struct {
	attempts int
	next     time.Time
}

type Worker struct {
	client                     *redis.Client
	processor                  Processor
	reviewer                   Reviewer
	stream, group, consumer    string
	initialized                bool
	turn                       int
	pendingCursor, claimCursor string
	claimIdle                  time.Duration
	retries                    map[string]retryState
	lastMessageID              string
	// Package-private barriers make transaction/ACK windows observable in real
	// integration tests. Production constructors leave them unset.
	afterCommit func(context.Context, redis.XMessage) error
}

// UseReviewer attaches the durable review store. Without it the worker keeps an
// unknown outcome pending forever, which is safe but not operable.
func (worker *Worker) UseReviewer(reviewer Reviewer) { worker.reviewer = reviewer }

func NewWorker(client *redis.Client, processor Processor) *Worker {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		panic("order consumer identity unavailable")
	}
	return newWorker(client, processor, "stream.orders", "g1", "order-go-"+hex.EncodeToString(entropy[:]))
}

func newWorker(client *redis.Client, processor Processor, stream, group, consumer string) *Worker {
	return &Worker{client: client, processor: processor, stream: stream, group: group, consumer: consumer,
		pendingCursor: "0", claimCursor: "0-0", claimIdle: time.Minute, retries: make(map[string]retryState)}
}

func (worker *Worker) Run(ctx context.Context) {
	emptySteps := 0
	for ctx.Err() == nil {
		processed, err := worker.Poll(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// Driver errors can include request contents; the durable PEL is the
			// diagnostic source rather than arbitrary exception text in logs.
			if processed {
				slog.Warn("order delivery remains pending", "consumer", worker.consumer, "message_id", worker.lastMessageID)
			} else {
				slog.Warn("order stream read unavailable", "consumer", worker.consumer)
			}
		}
		if processed && err == nil {
			emptySteps = 0
			continue
		}
		if err == nil {
			emptySteps++
			if emptySteps < 3 {
				continue
			}
		}
		emptySteps = 0
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Poll fairly rotates new messages, this consumer's PEL and idle claims. A bad
// first pending row cannot starve later pending rows or new reservations.
func (worker *Worker) Poll(ctx context.Context) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if !worker.initialized {
		err := worker.client.XGroupCreateMkStream(readCtx, worker.stream, worker.group, "0").Err()
		if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP ") {
			return false, errors.New("order stream group unavailable")
		}
		worker.initialized = true
	}
	var message *redis.XMessage
	var err error
	switch worker.turn {
	case 0:
		message, err = worker.read(readCtx, ">")
	case 1:
		message, err = worker.read(readCtx, worker.pendingCursor)
		if message == nil {
			worker.pendingCursor = "0"
		} else {
			worker.pendingCursor = message.ID
		}
	case 2:
		var rows []redis.XMessage
		var nextCursor string
		rows, nextCursor, err = worker.client.XAutoClaim(readCtx, &redis.XAutoClaimArgs{
			Stream: worker.stream, Group: worker.group, Consumer: worker.consumer, MinIdle: worker.claimIdle,
			Start: worker.claimCursor, Count: 1,
		}).Result()
		// Failed replies carry an empty cursor. Retain the last known position
		// so a transient Redis failure cannot permanently break future claims.
		if err == nil && nextCursor != "" {
			worker.claimCursor = nextCursor
		}
		if len(rows) > 0 {
			message = &rows[0]
		}
	}
	worker.turn = (worker.turn + 1) % 3
	if err != nil && !errors.Is(err, redis.Nil) {
		if strings.HasPrefix(err.Error(), "NOGROUP ") {
			worker.initialized = false
		}
		return false, errors.New("order stream read unavailable")
	}
	if message == nil {
		return false, nil
	}
	worker.lastMessageID = message.ID
	if backoff, ok := worker.retries[message.ID]; ok && time.Now().Before(backoff.next) {
		return false, nil
	}
	err = worker.deliver(ctx, *message)
	if err != nil {
		previous := worker.retries[message.ID]
		if previous.attempts < reviewThreshold {
			previous.attempts++
		}
		previous.next = time.Now().Add(time.Duration(1<<previous.attempts) * time.Second)
		// Recovery scheduling is bounded memory; durable evidence stays in PEL.
		if len(worker.retries) >= 1024 {
			clear(worker.retries)
		}
		worker.retries[message.ID] = previous
		// Past the threshold the outcome must be parked durably before the ACK.
		// If the insert fails (database still down) the message stays pending.
		if previous.attempts >= reviewThreshold && worker.reviewer != nil {
			if parked := worker.parkForReview(ctx, *message, previous.attempts, err); parked == nil {
				delete(worker.retries, message.ID)
				return true, nil
			}
		}
		return true, err
	}
	delete(worker.retries, message.ID)
	return true, nil
}

// parkForReview writes the durable review row and only then acknowledges the
// message. The row, not the PEL entry, is what the replay command reads.
func (worker *Worker) parkForReview(ctx context.Context, message redis.XMessage, attempts int, cause error) error {
	id, idOK := field(message, "id", true)
	user, userOK := field(message, "userId", false)
	voucher, voucherOK := field(message, "voucherId", false)
	if !idOK || !userOK || !voucherOK {
		return errors.New("invalid message cannot be parked for review")
	}
	recordCtx, stopRecord := context.WithTimeout(ctx, 3*time.Second)
	defer stopRecord()
	if err := worker.reviewer.Record(recordCtx, id, user, voucher, message.ID, attempts, cause); err != nil {
		return errors.New("unknown outcome and review record not durable")
	}
	ackCtx, stopAck := context.WithTimeout(ctx, time.Second)
	defer stopAck()
	if err := worker.client.XAck(ackCtx, worker.stream, worker.group, message.ID).Err(); err != nil {
		return errors.New("durable review awaiting ACK retry")
	}
	return nil
}

func (worker *Worker) read(ctx context.Context, position string) (*redis.XMessage, error) {
	rows, err := worker.client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: worker.group, Consumer: worker.consumer,
		Streams: []string{worker.stream, position}, Count: 1, Block: -1}).Result()
	if err != nil {
		return nil, err
	}
	for _, stream := range rows {
		if len(stream.Messages) > 0 {
			return &stream.Messages[0], nil
		}
	}
	return nil, nil
}

func field(message redis.XMessage, name string, signed bool) (string, bool) {
	value, ok := message.Values[name].(string)
	if !ok {
		return "", false
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != value || (signed && parsed > 9223372036854775807) {
		return "", false
	}
	return value, true
}

func (worker *Worker) deliver(ctx context.Context, message redis.XMessage) error {
	id, idOK := field(message, "id", true)
	user, userOK := field(message, "userId", false)
	voucher, voucherOK := field(message, "voucherId", false)
	if !idOK || !userOK || !voucherOK {
		return errors.New("invalid order message retained in PEL")
	}
	createCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	outcome, err := worker.processor.Create(createCtx, id, user, voucher)
	cancel()
	if err != nil {
		return errors.New("order persistence unconfirmed")
	}
	if outcome.State != "CREATED" && outcome.State != "FAILED" {
		return errors.New("order outcome has no durable proof")
	}
	// The order (or the rejection event) is already durable, so a failed cleanup
	// only keeps the review row blocking a rebuild; it must not undo the ACK.
	if worker.reviewer != nil {
		resolveCtx, stopResolve := context.WithTimeout(ctx, time.Second)
		_ = worker.reviewer.Resolve(resolveCtx, id)
		stopResolve()
	}
	if worker.afterCommit != nil {
		if err := worker.afterCommit(ctx, message); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// This projection is optional: DB order/rejection is already authoritative.
	// A projection failure leaves the durable outbox for later recovery.
	projectionCtx, stopProjection := context.WithTimeout(ctx, 500*time.Millisecond)
	_ = projectReservation.Run(projectionCtx, worker.client, []string{"order:reservation:" + id}, user, voucher, outcome.State).Err()
	stopProjection()
	ackCtx, stopAck := context.WithTimeout(ctx, time.Second)
	defer stopAck()
	if err := worker.client.XAck(ackCtx, worker.stream, worker.group, message.ID).Err(); err != nil {
		return errors.New("durable order awaiting ACK retry")
	}
	return nil
}

var projectReservation = redis.NewScript(`
if #KEYS~=1 or #ARGV~=3 then return 0 end
if ARGV[3]~='CREATED' and ARGV[3]~='FAILED' then return 0 end
if redis.call('TYPE',KEYS[1]).ok~='hash' then return 0 end
if redis.call('HGET',KEYS[1],'userId')~=ARGV[1] or redis.call('HGET',KEYS[1],'voucherId')~=ARGV[2] then return 0 end
redis.call('HSET',KEYS[1],'state',ARGV[3])
redis.call('EXPIRE',KEYS[1],86400)
return 1
`)
