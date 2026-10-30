package orderstream

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/order"
	"github.com/cityloop/fullstack-go/internal/orderreview"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

type streamFixture struct {
	r                         *database.Resources
	id, user, voucher, stream string
	begin, end                time.Time
}

func newFixture(t *testing.T, r *database.Resources) *streamFixture {
	t.Helper()
	var entropy [12]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatUint(9007199254740993+binary.LittleEndian.Uint64(entropy[:8])%1000000000, 10)
	f := &streamFixture{r: r, id: id, user: id, voucher: id, stream: "stream.go030.test." + hex.EncodeToString(entropy[:])}
	f.begin = time.Now().In(time.FixedZone("Shanghai", 8*3600)).Truncate(time.Second).Add(-time.Minute)
	f.end = f.begin.Add(time.Hour)
	var count int64
	if r.ORM.Raw("SELECT COUNT(*) FROM tb_voucher WHERE id=?", id).Scan(&count).Error != nil || count != 0 {
		t.Fatal("owned fixture ID collision")
	}
	f.exec(t, `INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES(?,?,'Go Stream fixture',990,2000,1,1)`, id, id)
	f.exec(t, `INSERT INTO tb_seckill_voucher(voucher_id,merchant_id,stock,total_stock,publish_status,publish_version,begin_time,end_time,redeem_begin_time,redeem_end_time)
VALUES(?,?,10,10,'ACTIVE',1,?,?,?,?)`, id, id, f.begin, f.end, f.begin, f.end.Add(time.Hour))
	if err := r.Redis.HSet(context.Background(), "order:reservation:"+id, "userId", id, "voucherId", id, "state", "PROCESSING").Err(); err != nil {
		t.Fatal("cannot seed owned reservation")
	}
	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM tb_outbox WHERE (aggregate_type='ORDER' OR aggregate_type='RESERVATION') AND aggregate_id=?",
			"DELETE FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CREATE'",
			"DELETE FROM tb_voucher_order WHERE id=?", "DELETE FROM tb_seckill_voucher WHERE voucher_id=?", "DELETE FROM tb_voucher WHERE id=?",
		} {
			if r.ORM.Exec(q, id).Error != nil {
				t.Error("cannot remove exact Stream fixture rows")
			}
		}
		if r.Redis.Del(context.Background(), f.stream, "order:reservation:"+id, "seckill:stock:"+id).Err() != nil {
			t.Error("cannot remove exact Stream fixture keys")
		}
	})
	return f
}

func (f *streamFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if f.r.ORM.Exec(q, args...).Error != nil {
		t.Fatal("owned Stream fixture SQL failed")
	}
}
func (f *streamFixture) count(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if f.r.ORM.Raw(q, args...).Scan(&n).Error != nil {
		t.Fatal("owned Stream fixture read failed")
	}
	return n
}
func (f *streamFixture) add(t *testing.T) string {
	t.Helper()
	id, err := f.r.Redis.XAdd(context.Background(), &redis.XAddArgs{Stream: f.stream, Values: map[string]any{"id": f.id, "userId": f.user, "voucherId": f.voucher}}).Result()
	if err != nil {
		t.Fatal("cannot add owned message")
	}
	return id
}
func (f *streamFixture) worker(name string) *Worker {
	return newWorker(f.r.Redis, order.NewCreator(f.r.ORM, 15*time.Minute), f.stream, "g1", name)
}
func (f *streamFixture) pending(t *testing.T) int64 {
	t.Helper()
	p, err := f.r.Redis.XPending(context.Background(), f.stream, "g1").Result()
	if err != nil {
		t.Fatal("cannot inspect pending")
	}
	return p.Count
}
func (f *streamFixture) unread(t *testing.T) int64 {
	t.Helper()
	groups, err := f.r.Redis.XInfoGroups(context.Background(), f.stream).Result()
	if err != nil {
		t.Fatal("cannot inspect owned Stream unread count")
	}
	for _, group := range groups {
		if group.Name == "g1" {
			return group.Lag
		}
	}
	t.Fatal("owned Stream group missing")
	return -1
}
func (f *streamFixture) requireCreated(t *testing.T, stock int64) {
	t.Helper()
	if f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", f.id) != 1 || f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", f.voucher) != stock || f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CREATE'", f.id) != 1 || f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id=? AND event_type='ORDER_CHANGED'", f.id) != 1 || f.pending(t) != 0 {
		t.Fatal("order, inventory, audit, event and ACK did not converge exactly once")
	}
}

// Extra messages share the fixture's activity but own exact additional order
// and reservation IDs. Cleanup never touches another activity or stream.
func (f *streamFixture) addOrder(t *testing.T, id, user string) string {
	t.Helper()
	if f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", id) != 0 {
		t.Fatal("extra owned order ID collision")
	}
	t.Cleanup(func() {
		for _, query := range []string{
			"DELETE FROM tb_order_review WHERE order_id=?",
			"DELETE FROM tb_outbox WHERE aggregate_id=? AND aggregate_type IN ('ORDER','RESERVATION')",
			"DELETE FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CREATE'",
			"DELETE FROM tb_voucher_order WHERE id=?",
		} {
			if f.r.ORM.Exec(query, id).Error != nil {
				t.Error("cannot remove exact extra order fixture")
			}
		}
		if f.r.Redis.Del(context.Background(), "order:reservation:"+id).Err() != nil {
			t.Error("cannot remove exact extra reservation")
		}
	})
	if err := f.r.Redis.HSet(context.Background(), "order:reservation:"+id,
		"userId", user, "voucherId", f.voucher, "state", "PROCESSING").Err(); err != nil {
		t.Fatal("cannot seed extra owned reservation")
	}
	message, err := f.r.Redis.XAdd(context.Background(), &redis.XAddArgs{Stream: f.stream,
		Values: map[string]any{"id": id, "userId": user, "voucherId": f.voucher}}).Result()
	if err != nil {
		t.Fatal("cannot add extra owned message")
	}
	return message
}

func runFixtureWorkers(t *testing.T, workers ...*Worker) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wait sync.WaitGroup
	for _, worker := range workers {
		wait.Add(1)
		go func() { defer wait.Done(); worker.Run(ctx) }()
	}
	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("not all order workers exited after cancellation")
		}
	}
	t.Cleanup(stop)
	return stop
}

func awaitStream(t *testing.T, ready func() bool) {
	t.Helper()
	// Concurrent same-ID GET_LOCK(0) contention deliberately remains pending.
	// Four serial winners can require the existing 2s+4s+8s retry schedule.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("concurrent Stream delivery did not converge")
}

type gatedCreator struct {
	creator Processor
	entered chan<- struct{}
	release <-chan struct{}
}

func (gate gatedCreator) Create(ctx context.Context, id, user, voucher string) (order.Outcome, error) {
	select {
	case gate.entered <- struct{}{}:
	case <-ctx.Done():
		return order.Outcome{}, ctx.Err()
	}
	select {
	case <-gate.release:
		return gate.creator.Create(ctx, id, user, voucher)
	case <-ctx.Done():
		return order.Outcome{}, ctx.Err()
	}
}

func TestIntegrationOrderStream(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL/Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	r, err := database.Open(ctx, cfg)
	cancel()
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() {
		if r.Close() != nil {
			t.Error("dependency close failed")
		}
	})
	ctx = context.Background()
	t.Run("four_workers_share_creator_and_reviewer_without_losing_inventory_or_ack", func(t *testing.T) {
		f := newFixture(t, r)
		const orders = 8
		base, _ := strconv.ParseUint(f.id, 10, 64)
		ids := make([]string, orders)
		ids[0] = f.id
		f.add(t)
		for i := 1; i < orders; i++ {
			ids[i] = strconv.FormatUint(base+uint64(i), 10)
			f.addOrder(t, ids[i], ids[i])
		}
		entered, release := make(chan struct{}, orders), make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(release) })
		shared := gatedCreator{order.NewCreator(r.ORM, 15*time.Minute), entered, release}
		reviewer := orderreview.NewRepository(r.ORM)
		workers := make([]*Worker, 4)
		for i := range workers {
			workers[i] = newWorker(r.Redis, shared, f.stream, "g1", "parallel-"+strconv.Itoa(i))
			workers[i].UseReviewer(reviewer)
		}
		stop := runFixtureWorkers(t, workers...)
		// Each worker is serial: four entries here prove four independent
		// consumers entered the shared Creator concurrently, before any commit.
		for range workers {
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("four independent consumers did not enter the shared processor")
			}
		}
		releaseOnce.Do(func() { close(release) })
		awaitStream(t, func() bool {
			return f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id=?", f.voucher) == orders && f.pending(t) == 0
		})
		stop()
		if f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", f.voucher) != 2 ||
			f.count(t, "SELECT COUNT(DISTINCT user_id) FROM tb_voucher_order WHERE voucher_id=? AND active_flag=1", f.voucher) != orders {
			t.Fatal("parallel creation lost stock or unique active ownership")
		}
		for _, id := range ids {
			if f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND user_id=? AND voucher_id=?", id, id, f.voucher) != 1 ||
				f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CREATE'", id) != 1 ||
				f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=? AND aggregate_type='ORDER' AND event_type='ORDER_CHANGED'", id) != 1 {
				t.Fatal("parallel order, owner, audit and outbox did not match exactly once")
			}
			if state, err := r.Redis.HGet(ctx, "order:reservation:"+id, "state").Result(); err != nil || state != "CREATED" {
				t.Fatal("ACKed order lacks its matching reservation projection")
			}
		}
	})
	t.Run("four_workers_ack_same_id_replays_but_retain_owner_conflict", func(t *testing.T) {
		f := newFixture(t, r)
		for range 4 {
			f.add(t)
		}
		creator, reviewer := order.NewCreator(r.ORM, 15*time.Minute), orderreview.NewRepository(r.ORM)
		workers := make([]*Worker, 4)
		for i := range workers {
			workers[i] = newWorker(r.Redis, creator, f.stream, "g1", "duplicate-"+strconv.Itoa(i))
			workers[i].UseReviewer(reviewer)
		}
		stop := runFixtureWorkers(t, workers...)
		awaitStream(t, func() bool {
			return f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", f.id) == 1 && f.pending(t) == 0 && f.unread(t) == 0
		})
		base, _ := strconv.ParseUint(f.id, 10, 64)
		foreign := strconv.FormatUint(base+1, 10)
		conflict, err := r.Redis.XAdd(ctx, &redis.XAddArgs{Stream: f.stream,
			Values: map[string]any{"id": f.id, "userId": foreign, "voucherId": f.voucher}}).Result()
		if err != nil {
			t.Fatal("cannot add owned conflicting message")
		}
		secondID := strconv.FormatUint(base+2, 10)
		f.addOrder(t, secondID, f.user) // another ID for an already active owner
		awaitStream(t, func() bool {
			return f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=? AND event_type='RESERVATION_REJECTED'", secondID) == 1 && f.pending(t) == 1 && f.unread(t) == 0
		})
		stop()
		pending, err := r.Redis.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: f.stream, Group: "g1", Start: "-", End: "+", Count: 10}).Result()
		if err != nil || len(pending) != 1 || pending[0].ID != conflict {
			t.Fatal("a foreign binding was mistaken for the owner's durable result")
		}
		if f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", f.voucher) != 9 ||
			f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id=?", f.voucher) != 1 ||
			f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=? AND user_id=?", f.id, f.user) != 1 ||
			f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ORDER_CREATE'", f.id) != 1 ||
			f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=? AND aggregate_type='ORDER'", f.id) != 1 ||
			f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=? AND event_type='RESERVATION_REJECTED'", f.id) != 0 {
			t.Fatal("duplicate or conflicting delivery changed the single committed fact")
		}
	})
	t.Run("db_fact_overrides_a_cached_failed_reservation", func(t *testing.T) {
		f := newFixture(t, r)
		f.add(t)
		if r.Redis.HSet(ctx, "order:reservation:"+f.id, "state", "FAILED").Err() != nil {
			t.Fatal("cannot arrange stale projection")
		}
		if processed, err := f.worker("first").Poll(ctx); !processed || err != nil {
			t.Fatal("valid message not persisted")
		}
		f.requireCreated(t, 9)
		view, err := order.NewService(r.ORM, r.Redis, 15*time.Minute).Get(ctx, f.user, f.id)
		if err != nil || view.State != "CREATED" || view.OrderStatus == nil || *view.OrderStatus != 1 {
			t.Fatal("query must use the durable order")
		}
	})
	t.Run("cancel_after_order_insert_before_commit_rolls_back_and_reclaims", func(t *testing.T) {
		f := newFixture(t, r)
		sourceID := f.add(t)
		entered := make(chan struct{})
		var once atomic.Bool
		name := "go030_precommit_" + f.id
		if err := r.ORM.Callback().Create().After("gorm:create").Register(name, func(db *gorm.DB) {
			if db.Statement.Table == "tb_voucher_order" && once.CompareAndSwap(false, true) {
				close(entered)
				<-db.Statement.Context.Done()
				db.AddError(db.Statement.Context.Err())
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.ORM.Callback().Create().Remove(name) })
		worker := f.worker("before-commit")
		runCtx, stop := context.WithCancel(ctx)
		defer stop()
		finished := make(chan error, 1)
		go func() { _, e := worker.Poll(runCtx); finished <- e }()
		select {
		case <-entered:
			t.Logf("barrier=after-insert-before-commit orderId=%s sourceId=%s consumer=before-commit at=%s", f.id, sourceID, time.Now().UTC().Format(time.RFC3339Nano))
		case <-time.After(3 * time.Second):
			t.Fatal("did not reach real insert before commit")
		}
		stop()
		select {
		case e := <-finished:
			if e == nil {
				t.Fatal("interrupted transaction reported a durable result")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled worker did not exit")
		}
		if f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", f.id) != 0 || f.count(t, "SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", f.voucher) != 10 || f.pending(t) != 1 {
			t.Fatal("precommit cancellation lost pending or left partial DB data")
		}
		t.Logf("after-cancel orderId=%s dbOrders=0 dbStock=10 pending=%d at=%s", f.id, f.pending(t), time.Now().UTC().Format(time.RFC3339Nano))
		recovered := f.worker("after-precommit")
		recovered.claimIdle = 0
		recovered.turn = 2
		if processed, e := recovered.Poll(ctx); !processed || e != nil {
			t.Fatal("new consumer did not reclaim interrupted transaction")
		}
		f.requireCreated(t, 9)
		t.Logf("after-reclaim orderId=%s sourceId=%s consumer=after-precommit dbOrders=1 dbStock=9 pending=%d at=%s", f.id, sourceID, f.pending(t), time.Now().UTC().Format(time.RFC3339Nano))
	})
	t.Run("cancel_after_commit_before_ack_replays_current_order_without_decrement", func(t *testing.T) {
		f := newFixture(t, r)
		sourceID := f.add(t)
		entered := make(chan struct{})
		worker := f.worker("before-ack")
		worker.afterCommit = func(ctx context.Context, _ redis.XMessage) error { close(entered); <-ctx.Done(); return ctx.Err() }
		runCtx, stop := context.WithCancel(ctx)
		defer stop()
		done := make(chan error, 1)
		go func() { _, e := worker.Poll(runCtx); done <- e }()
		select {
		case <-entered:
			t.Logf("barrier=after-commit-before-ack orderId=%s sourceId=%s consumer=before-ack at=%s", f.id, sourceID, time.Now().UTC().Format(time.RFC3339Nano))
		case <-time.After(3 * time.Second):
			t.Fatal("did not reach committed-before-ACK barrier")
		}
		if f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", f.id) != 1 || f.pending(t) != 1 {
			t.Fatal("window did not contain committed order and pending message")
		}
		stop()
		select {
		case e := <-done:
			if e == nil {
				t.Fatal("interrupted ACK unexpectedly succeeded")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("worker did not cancel")
		}
		// Controlled fixture simulation of a later cancelled order, not a claim
		// that the GO-031 cancellation endpoint already exists.
		f.exec(t, "UPDATE tb_voucher_order SET status=4 WHERE id=?", f.id)
		f.exec(t, "UPDATE tb_seckill_voucher SET stock=10 WHERE voucher_id=?", f.voucher)
		creator, reviewer := order.NewCreator(r.ORM, 15*time.Minute), orderreview.NewRepository(r.ORM)
		recovered := make([]*Worker, 4)
		for i := range recovered {
			recovered[i] = newWorker(r.Redis, creator, f.stream, "g1", "after-ack-"+strconv.Itoa(i))
			recovered[i].UseReviewer(reviewer)
			recovered[i].claimIdle = 0
			recovered[i].turn = 2
		}
		stopRecovered := runFixtureWorkers(t, recovered...)
		awaitStream(t, func() bool { return f.pending(t) == 0 })
		stopRecovered()
		f.requireCreated(t, 10)
		if f.count(t, "SELECT status FROM tb_voucher_order WHERE id=?", f.id) != 4 {
			t.Fatal("old message reset a later order state")
		}
		t.Logf("after-replay orderId=%s sourceId=%s consumer=after-ack status=4 dbStock=10 pending=%d at=%s", f.id, sourceID, f.pending(t), time.Now().UTC().Format(time.RFC3339Nano))
	})
	t.Run("unavailable_db_beyond_five_attempts_never_acks_then_recovers", func(t *testing.T) {
		f := newFixture(t, r)
		f.add(t)
		closed, e := gorm.Open(gormmysql.New(gormmysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
		if e != nil {
			t.Fatal("cannot arrange independent closed pool")
		}
		pool, e := closed.DB()
		if e != nil {
			t.Fatal(e)
		}
		_ = pool.Close()
		worker := newWorker(r.Redis, order.NewCreator(closed, 15*time.Minute), f.stream, "g1", "db-down")
		for attempt := 0; attempt < 8; attempt++ {
			clear(worker.retries)
			worker.turn = 1
			worker.pendingCursor = "0"
			if attempt == 0 {
				worker.turn = 0
			}
			processed, e := worker.Poll(ctx)
			if !processed || e == nil {
				t.Fatal("DB failure did not remain unconfirmed")
			}
		}
		if f.pending(t) != 1 || f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", f.id) != 0 || f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=?", f.id) != 0 {
			t.Fatal("retry threshold fabricated a durable rejection or ACK")
		}
		recovered := f.worker("db-restored")
		recovered.turn = 2
		recovered.claimIdle = 0
		if _, e := recovered.Poll(ctx); e != nil {
			t.Fatal("healthy consumer failed to reclaim")
		}
		f.requireCreated(t, 9)
	})
	t.Run("poison_pending_does_not_starve_later_pending_or_new_messages", func(t *testing.T) {
		f := newFixture(t, r)
		if r.Redis.XAdd(ctx, &redis.XAddArgs{Stream: f.stream, Values: map[string]any{"id": "bad"}}).Err() != nil {
			t.Fatal("cannot seed poison")
		}
		worker := f.worker("fair")
		if _, e := worker.Poll(ctx); e == nil {
			t.Fatal("bad message was not retained")
		}
		f.add(t)
		// Put the valid later message into this same consumer's PEL first;
		// processing must advance past the poison entry rather than ID 0 forever.
		if r.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "g1", Consumer: "fair", Streams: []string{f.stream, ">"}, Count: 1, Block: -1}).Err() != nil {
			t.Fatal("cannot arrange later own pending message")
		}
		for i := 0; i < 20 && f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", f.id) == 0; i++ {
			_, _ = worker.Poll(ctx)
		}
		if f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", f.id) != 1 || f.pending(t) != 1 {
			t.Fatal("poison starved new message or was ACKed without proof")
		}
		fresh := newFixture(t, r)
		if r.Redis.XAdd(ctx, &redis.XAddArgs{Stream: f.stream, Values: map[string]any{"id": fresh.id, "userId": fresh.user, "voucherId": fresh.voucher}}).Err() != nil {
			t.Fatal("cannot add new reservation after poison")
		}
		for i := 0; i < 20 && fresh.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", fresh.id) == 0; i++ {
			_, _ = worker.Poll(ctx)
		}
		if fresh.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", fresh.id) != 1 || f.pending(t) != 1 {
			t.Fatal("poison starved a new reservation")
		}
	})
	t.Run("durable_rejection_is_ackable_and_compensation_remains_pending", func(t *testing.T) {
		f := newFixture(t, r)
		f.exec(t, "UPDATE tb_seckill_voucher SET stock=0 WHERE voucher_id=?", f.voucher)
		f.add(t)
		if r.Redis.Set(ctx, "seckill:stock:"+f.voucher, 7, 0).Err() != nil {
			t.Fatal("cannot seed stock")
		}
		if _, e := f.worker("rejected").Poll(ctx); e != nil {
			t.Fatal("committed rejection not delivered")
		}
		if f.pending(t) != 0 || f.count(t, "SELECT COUNT(*) FROM tb_voucher_order WHERE id=?", f.id) != 0 || f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='RESERVATION' AND aggregate_id=? AND event_type='RESERVATION_REJECTED' AND delivery_status='PENDING'", f.id) != 1 {
			t.Fatal("rejection lacks its durable compensation event")
		}
		if value, e := r.Redis.Get(ctx, "seckill:stock:"+f.voucher).Result(); e != nil || value != "7" {
			t.Fatal("GO-030 consumer performed unsupported compensation")
		}
	})
	t.Run("transient_claim_error_does_not_destroy_recovery_cursor", func(t *testing.T) {
		f := newFixture(t, r)
		f.add(t)
		if r.Redis.XGroupCreateMkStream(ctx, f.stream, "g1", "0").Err() != nil {
			t.Fatal("cannot create test group")
		}
		if r.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "g1", Consumer: "gone", Streams: []string{f.stream, ">"}, Count: 1, Block: -1}).Err() != nil {
			t.Fatal("cannot arrange old PEL")
		}
		bad := redis.NewClient(r.Redis.Options())
		_ = bad.Close()
		worker := newWorker(bad, order.NewCreator(r.ORM, 15*time.Minute), f.stream, "g1", "recovered")
		worker.initialized = true
		worker.turn = 2
		worker.claimIdle = 0
		if _, e := worker.Poll(ctx); e == nil || worker.claimCursor != "0-0" {
			t.Fatal("failed claim overwrote its recovery cursor")
		}
		worker.client = r.Redis
		worker.turn = 2
		if processed, e := worker.Poll(ctx); !processed || e != nil {
			t.Fatal("claim did not recover after transient Redis failure")
		}
		f.requireCreated(t, 9)
	})
	t.Run("cancelled_run_returns_without_group_mutation", func(t *testing.T) {
		f := newFixture(t, r)
		worker := f.worker("cancelled")
		cancelled, stop := context.WithCancel(ctx)
		stop()
		done := make(chan struct{})
		go func() { worker.Run(cancelled); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Run ignored cancellation")
		}
		if n, e := r.Redis.Exists(ctx, f.stream).Result(); e != nil || n != 0 {
			t.Fatal("cancelled worker created a stream")
		}
	})
}
