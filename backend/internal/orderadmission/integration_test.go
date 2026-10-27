package orderadmission

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cityloop/fullstack-go/internal/activitypub"
	"github.com/cityloop/fullstack-go/internal/platform/config"
)

func integrationRedis(t *testing.T) *redis.Client {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with explicitly configured isolated Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated integration configuration")
	}
	client := redis.NewClient(&redis.Options{Addr: net.JoinHostPort(cfg.Redis.Host, strconv.Itoa(cfg.Redis.Port)),
		Password: cfg.Redis.Password, DB: cfg.Redis.Database, DialTimeout: time.Second,
		ReadTimeout: time.Second, WriteTimeout: time.Second, ContextTimeoutEnabled: true, MaxRetries: -1})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if client.Ping(ctx).Err() != nil {
		_ = client.Close()
		t.Fatal("isolated integration Redis unavailable")
	}
	t.Cleanup(func() {
		if client.Close() != nil {
			t.Error("cannot close integration Redis client")
		}
	})
	return client
}

type fixture struct {
	client          *redis.Client
	admission       *Admission
	voucherID       string
	base            uint64
	now, begin, end time.Time
	owned           map[string]bool
}

func newFixture(t *testing.T, client *redis.Client, stock int) *fixture {
	t.Helper()
	var entropy [12]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("cannot allocate independent admission fixture")
	}
	base := uint64(9_007_199_254_740_993) + binary.LittleEndian.Uint64(entropy[:8])%1_000_000_000
	now := time.Now().In(businessZone).Truncate(time.Second)
	admission := New(client)
	admission.stream = "go030:admission:stream:" + hex.EncodeToString(entropy[:])
	admission.counterPrefix = "icr:go030:" + hex.EncodeToString(entropy[:]) + ":"
	f := &fixture{client: client, admission: admission, voucherID: strconv.FormatUint(base, 10), base: base,
		now: now, begin: now.Add(-time.Minute), end: now.Add(time.Hour), owned: make(map[string]bool)}
	for _, key := range admission.keys(f.voucherID, f.candidate(0)) {
		f.own(t, key)
	}
	t.Cleanup(func() {
		keys := make([]string, 0, len(f.owned))
		for key := range f.owned {
			keys = append(keys, key)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if len(keys) > 0 && client.Del(ctx, keys...).Err() != nil {
			t.Error("cannot remove exact admission fixture keys")
		}
		if count, err := client.Exists(ctx, keys...).Result(); err != nil || count != 0 {
			t.Error("admission fixture cleanup left owned keys")
		}
	})
	gate := activitypub.NewGate(client)
	if gate.Prepare(context.Background(), f.voucherID, 1, stock, f.begin, f.end) != nil || gate.Open(context.Background(), f.voucherID, 1) != nil {
		t.Fatal("cannot prepare isolated admission fixture")
	}
	return f
}

func (f *fixture) own(t *testing.T, key string) {
	t.Helper()
	if f.owned[key] {
		return
	}
	if count, err := f.client.Exists(context.Background(), key).Result(); err != nil || count != 0 {
		t.Fatal("cannot establish ownership of random fixture key")
	}
	f.owned[key] = true
}

func (f *fixture) candidate(index int) string {
	return strconv.FormatUint(f.base+uint64(index)+1000, 10)
}
func (f *fixture) user(index int) string { return strconv.FormatUint(f.base+uint64(index)+2000, 10) }

func (f *fixture) reserve(t *testing.T, index int) (Result, error) {
	t.Helper()
	f.own(t, "order:reservation:"+f.candidate(index))
	return f.admission.Reserve(context.Background(), f.voucherID, f.user(index), f.candidate(index), f.now, f.begin, f.end, 1)
}

func dumpKeys(t *testing.T, client *redis.Client, keys []string) []string {
	t.Helper()
	values := make([]string, len(keys))
	for index, key := range keys {
		value, err := client.Dump(context.Background(), key).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			t.Fatal("cannot snapshot admission fixture")
		}
		values[index] = value
	}
	return values
}

func TestIntegrationAdmission(t *testing.T) {
	client := integrationRedis(t)
	ctx := context.Background()
	t.Run("all_eight_wrong_key_types_fail_without_partial_writes_and_recover_after_repair", func(t *testing.T) {
		for index := 0; index < 8; index++ {
			t.Run(strconv.Itoa(index), func(t *testing.T) {
				f := newFixture(t, client, 5)
				keys := f.admission.keys(f.voucherID, f.candidate(0))
				if client.Del(ctx, keys[index]).Err() != nil || client.LPush(ctx, keys[index], "wrong type").Err() != nil {
					t.Fatal("cannot arrange owned wrong type")
				}
				before := dumpKeys(t, client, keys)
				if _, err := f.reserve(t, 0); !errors.Is(err, ErrUnsafe) {
					t.Fatal("every dependency type must be checked before first write")
				}
				if !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
					t.Fatal("wrong-type admission partially changed inventory, eligibility, reservation, map or stream")
				}
				if client.Del(ctx, keys[index]).Err() != nil {
					t.Fatal("cannot repair exact owned wrong key")
				}
				switch index {
				case 0:
					if client.HSet(ctx, keys[0], "state", "OPEN", "phase", "OPEN", "version", "1").Err() != nil {
						t.Fatal("cannot repair gate")
					}
				case 1:
					if client.Set(ctx, keys[1], "5", 0).Err() != nil {
						t.Fatal("cannot repair stock")
					}
				case 2:
					if client.Set(ctx, keys[2], f.begin.UnixMilli(), 0).Err() != nil {
						t.Fatal("cannot repair begin")
					}
				case 3:
					if client.Set(ctx, keys[3], f.end.UnixMilli(), 0).Err() != nil {
						t.Fatal("cannot repair end")
					}
				}
				result, err := f.reserve(t, 0)
				if err != nil || result.ID != f.candidate(0) || result.Reused {
					t.Fatal("safe admission did not recover after key repair")
				}
				f.requireCounts(t, 4, 1)
			})
		}
	})
	t.Run("bad_argv_aliased_keys_and_noncanonical_stock_do_not_write", func(t *testing.T) {
		f := newFixture(t, client, 5)
		keys := f.admission.keys(f.voucherID, f.candidate(0))
		base := []any{f.voucherID, f.user(0), f.candidate(0), f.now.UnixMilli(), f.begin.UnixMilli(), f.end.UnixMilli(), 1}
		before := dumpKeys(t, client, keys)
		for _, test := range []struct {
			index int
			value any
		}{
			{0, "0"}, {0, "18446744073709551616"}, {1, "01"}, {1, "-1"}, {2, "9223372036854775808"},
			{3, "NaN"}, {3, 0}, {3, "9007199254740992"}, {4, 1.5}, {4, "01"}, {5, f.begin.UnixMilli()}, {6, 0}, {6, 1.5}, {6, "2147483648"},
		} {
			arguments := append([]any(nil), base...)
			arguments[test.index] = test.value
			reply, err := client.Eval(ctx, reserveSource, keys, arguments...).Slice()
			code, _, decodeErr := decode(reply)
			if err != nil || decodeErr != nil || code != 6 || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
				t.Fatal("invalid Lua input wrote partial admission state")
			}
		}
		aliased := append([]string(nil), keys...)
		aliased[7] = keys[1]
		if response, err := client.Eval(ctx, reserveSource, aliased, base...).Slice(); err != nil {
			t.Fatal("aliased key precheck raised a runtime error")
		} else if code, _, err := decode(response); err != nil || code != 6 {
			t.Fatal("aliased keys were not rejected")
		}
		for _, value := range []string{"01", "1.0", "+1", "-1", "1e2", "2147483648"} {
			if client.Set(ctx, keys[1], value, 0).Err() != nil {
				t.Fatal("cannot arrange invalid inventory")
			}
			before := dumpKeys(t, client, keys)
			if _, err := f.reserve(t, 0); !errors.Is(err, ErrUnsafe) || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
				t.Fatal("invalid integer inventory passed the precheck")
			}
		}
	})
	t.Run("candidate_reservation_collision_is_never_overwritten", func(t *testing.T) {
		f := newFixture(t, client, 5)
		keys := f.admission.keys(f.voucherID, f.candidate(0))
		if client.HSet(ctx, keys[5], "userId", f.user(99), "voucherId", f.voucherID, "state", "PROCESSING").Err() != nil {
			t.Fatal("cannot arrange collision")
		}
		before := dumpKeys(t, client, keys)
		if _, err := f.reserve(t, 0); !errors.Is(err, ErrUnsafe) || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
			t.Fatal("candidate ID collision overwrote an existing reservation")
		}
	})
	t.Run("exact_signed_maximum_id_and_ttls_match_the_wire_contract", func(t *testing.T) {
		f := newFixture(t, client, 5)
		id := "9223372036854775807"
		f.own(t, "order:reservation:"+id)
		earliest, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal("cannot observe isolated Redis clock")
		}
		result, err := f.admission.Reserve(ctx, f.voucherID, "18446744073709551615", id, f.now, f.begin, f.end, 1)
		if err != nil || result.ID != id || result.Reused {
			t.Fatal("signed maximum ID or unsigned user ID lost precision")
		}
		messages, err := client.XRange(ctx, f.admission.stream, "-", "+").Result()
		if err != nil || len(messages) != 1 || messages[0].Values["id"] != id || messages[0].Values["userId"] != "18446744073709551615" || messages[0].Values["voucherId"] != f.voucherID || len(messages[0].Values) != 3 {
			t.Fatal("stream fields changed or IDs lost precision")
		}
		reservation, err := client.HGetAll(ctx, "order:reservation:"+id).Result()
		if err != nil || len(reservation) != 5 || reservation["state"] != "PROCESSING" || reservation["userId"] != "18446744073709551615" || reservation["retries"] != "0" {
			t.Fatal("reservation fields differ from the legacy contract")
		}
		latest, err := client.Time(ctx).Result()
		created, createdErr := strconv.ParseInt(reservation["createdAt"], 10, 64)
		if err != nil || createdErr != nil || created < earliest.UnixMilli() || created > latest.UnixMilli() {
			t.Fatal("createdAt must record actual Redis admission time as decimal milliseconds")
		}
		for _, target := range []struct {
			key     string
			seconds int
		}{{"order:reservation:" + id, 86400}, {"seckill:accepted:" + f.voucherID, 604800}} {
			ttl, err := client.TTL(ctx, target.key).Result()
			if err != nil || ttl > time.Duration(target.seconds)*time.Second || ttl < time.Duration(target.seconds-5)*time.Second {
				t.Fatal("reservation or accepted-map TTL changed")
			}
		}
		if ttl, err := client.TTL(ctx, "seckill:order:"+f.voucherID).Result(); err != nil || ttl != -1 {
			t.Fatal("purchase markers must not silently expire")
		}
	})
	t.Run("same_user_twenty_requests_make_one_reservation_and_return_one_id", func(t *testing.T) {
		f := newFixture(t, client, 10)
		for index := 0; index < 20; index++ {
			f.own(t, "order:reservation:"+f.candidate(index))
		}
		var wait sync.WaitGroup
		start := make(chan struct{})
		results := make([]Result, 20)
		failures := make([]error, 20)
		for index := 0; index < 20; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				results[index], failures[index] = f.admission.Reserve(ctx, f.voucherID, f.user(0), f.candidate(index), f.now, f.begin, f.end, 1)
			}(index)
		}
		close(start)
		wait.Wait()
		id, fresh, reused := results[0].ID, 0, 0
		for index, result := range results {
			if failures[index] != nil || result.ID != id || id == "" {
				t.Fatal("concurrent retries must find the exact same accepted ID")
			}
			if result.Reused {
				reused++
			} else {
				fresh++
			}
		}
		if fresh != 1 || reused != 19 {
			t.Fatal("repeated successful lookups must not count as new admissions")
		}
		f.requireCounts(t, 9, 1)
	})
	t.Run("two_hundred_users_compete_for_one_hundred_inventory_without_oversell", func(t *testing.T) {
		f := newFixture(t, client, 100)
		for index := 0; index < 200; index++ {
			f.own(t, "order:reservation:"+f.candidate(index))
		}
		var wait sync.WaitGroup
		start := make(chan struct{})
		results := make([]Result, 200)
		failures := make([]error, 200)
		for index := 0; index < 200; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				results[index], failures[index] = f.admission.Reserve(ctx, f.voucherID, f.user(index), f.candidate(index), f.now, f.begin, f.end, 1)
			}(index)
		}
		close(start)
		wait.Wait()
		accepted, soldOut := 0, 0
		unique := map[string]bool{}
		for index, result := range results {
			if failures[index] == nil {
				if result.Reused || result.ID != f.candidate(index) || unique[result.ID] {
					t.Fatal("new multi-user admission reused or duplicated an order")
				}
				unique[result.ID] = true
				accepted++
			} else if errors.Is(failures[index], ErrSoldOut) {
				soldOut++
			} else {
				t.Fatalf("unexpected admission failure: %v", failures[index])
			}
		}
		if accepted != 100 || soldOut != 100 {
			t.Fatal("200 contenders did not produce exactly 100 unique new admissions and 100 sold-out outcomes")
		}
		f.requireCounts(t, 0, 100)
	})
	t.Run("closed_or_ended_sale_still_recovers_original_accepted_id_without_mutation", func(t *testing.T) {
		f := newFixture(t, client, 1)
		first, err := f.reserve(t, 0)
		if err != nil {
			t.Fatal("cannot arrange accepted response-loss case")
		}
		f.own(t, "order:reservation:"+f.candidate(1))
		if activitypub.NewGate(client).Close(ctx, f.voucherID, 2) != nil {
			t.Fatal("cannot close accepted fixture")
		}
		keys := append(f.admission.keys(f.voucherID, f.candidate(1)), "order:reservation:"+first.ID)
		before := dumpKeys(t, client, keys)
		result, err := f.admission.Reserve(ctx, f.voucherID, f.user(0), f.candidate(1), f.end.Add(time.Hour), f.begin, f.end, 2)
		if err != nil || !result.Reused || result.ID != first.ID || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
			t.Fatal("retrieving an old acceptance after closure performed a second reservation")
		}
		if recovered, err := f.admission.Recover(ctx, f.voucherID, f.user(0)); err != nil || recovered != first.ID {
			t.Fatal("accepted lookup failed after closure")
		}
		if recovered, err := f.admission.Recover(ctx, f.voucherID, f.user(99)); err != nil || recovered != "" {
			t.Fatal("another user acquired the original acceptance")
		}
	})
	t.Run("missing_time_bad_projection_or_max_stream_id_fail_closed", func(t *testing.T) {
		for _, action := range []string{"missing-stock", "missing-begin", "missing-end", "mismatched-time", "bad-stock", "bad-time", "legacy-phase", "wrong-version", "max-stream-id"} {
			t.Run(action, func(t *testing.T) {
				f := newFixture(t, client, 5)
				keys := f.admission.keys(f.voucherID, f.candidate(0))
				expected := ErrUnsafe
				switch action {
				case "missing-stock":
					if client.Del(ctx, keys[1]).Err() != nil {
						t.Fatal("fixture failed")
					}
				case "missing-begin":
					if client.Del(ctx, keys[2]).Err() != nil {
						t.Fatal("fixture failed")
					}
				case "missing-end":
					if client.Del(ctx, keys[3]).Err() != nil {
						t.Fatal("fixture failed")
					}
				case "mismatched-time":
					if client.Set(ctx, keys[2], f.begin.Add(time.Second).UnixMilli(), 0).Err() != nil {
						t.Fatal("fixture failed")
					}
				case "bad-stock":
					if client.Set(ctx, keys[1], "1.0", 0).Err() != nil {
						t.Fatal("fixture failed")
					}
				case "bad-time":
					if client.Set(ctx, keys[2], "NaN", 0).Err() != nil {
						t.Fatal("fixture failed")
					}
				case "legacy-phase":
					if client.HDel(ctx, keys[0], "phase").Err() != nil {
						t.Fatal("fixture failed")
					}
					expected = ErrClosed
				case "wrong-version":
					if client.HSet(ctx, keys[0], "version", 2).Err() != nil {
						t.Fatal("fixture failed")
					}
					expected = ErrClosed
				case "max-stream-id":
					if client.XAdd(ctx, &redis.XAddArgs{Stream: f.admission.stream, ID: "18446744073709551615-18446744073709551615", Values: map[string]any{"owned": "fixture"}}).Err() != nil {
						t.Fatal("cannot arrange exhausted stream ID")
					}
				}
				before := dumpKeys(t, client, keys)
				if _, err := f.reserve(t, 0); !errors.Is(err, expected) || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
					t.Fatal("unsafe or closed projection mutated admission state")
				}
			})
		}
	})
	t.Run("business_failures_are_distinct_and_read_only", func(t *testing.T) {
		for _, action := range []string{"sold-out", "already-purchased", "not-started", "ended", "closed"} {
			t.Run(action, func(t *testing.T) {
				f := newFixture(t, client, 5)
				keys := f.admission.keys(f.voucherID, f.candidate(0))
				now := f.now
				expected := ErrSoldOut
				switch action {
				case "sold-out":
					if client.Set(ctx, keys[1], 0, 0).Err() != nil {
						t.Fatal("fixture failed")
					}
				case "already-purchased":
					if client.SAdd(ctx, keys[4], f.user(0)).Err() != nil {
						t.Fatal("fixture failed")
					}
					expected = ErrAlreadyPurchased
				case "not-started":
					clock, err := client.Time(ctx).Result()
					if err != nil {
						t.Fatal("cannot observe isolated Redis clock")
					}
					f.begin = clock.Add(time.Minute).Truncate(time.Millisecond)
					f.end = f.begin.Add(time.Hour)
					if client.MSet(ctx, keys[2], f.begin.UnixMilli(), keys[3], f.end.UnixMilli()).Err() != nil {
						t.Fatal("cannot arrange future sales projection")
					}
					expected = ErrNotStarted
				case "ended":
					clock, err := client.Time(ctx).Result()
					if err != nil {
						t.Fatal("cannot observe isolated Redis clock")
					}
					f.end = clock.Add(-time.Minute).Truncate(time.Millisecond)
					f.begin = f.end.Add(-time.Hour)
					if client.MSet(ctx, keys[2], f.begin.UnixMilli(), keys[3], f.end.UnixMilli()).Err() != nil {
						t.Fatal("cannot arrange ended sales projection")
					}
					expected = ErrEnded
				case "closed":
					if activitypub.NewGate(client).Close(ctx, f.voucherID, 2) != nil {
						t.Fatal("fixture failed")
					}
					expected = ErrClosed
				}
				before := dumpKeys(t, client, keys)
				if _, err := f.admission.Reserve(ctx, f.voucherID, f.user(0), f.candidate(0), now, f.begin, f.end, 1); !errors.Is(err, expected) || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
					t.Fatal("business rejection had the wrong category or changed state")
				}
			})
		}
	})
}

func TestIntegrationAdmissionClockBoundary(t *testing.T) {
	client := integrationRedis(t)
	ctx := context.Background()
	setWindow := func(t *testing.T, f *fixture, begin, end time.Time) {
		t.Helper()
		f.begin, f.end = begin.Truncate(time.Millisecond), end.Truncate(time.Millisecond)
		if client.MSet(ctx, "seckill:begin:"+f.voucherID, f.begin.UnixMilli(), "seckill:end:"+f.voucherID, f.end.UnixMilli()).Err() != nil {
			t.Fatal("cannot set owned sales window")
		}
	}
	serverTime := func(t *testing.T) time.Time {
		t.Helper()
		now, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal("cannot observe isolated Redis clock")
		}
		return now
	}
	t.Run("stale_in_window_timestamp_cannot_admit_after_end", func(t *testing.T) {
		f := newFixture(t, client, 5)
		ending := serverTime(t).Add(-time.Second)
		setWindow(t, f, ending.Add(-time.Hour), ending)
		keys := f.admission.keys(f.voucherID, f.candidate(0))
		before := dumpKeys(t, client, keys)
		_, err := f.admission.Reserve(ctx, f.voucherID, f.user(0), f.candidate(0), f.end.Add(-time.Millisecond), f.begin, f.end, 1)
		if !errors.Is(err, ErrEnded) || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
			t.Fatalf("expired sale accepted a stale in-window timestamp or mutated state: %v", err)
		}
	})
	t.Run("in_window_argument_cannot_start_a_future_sale", func(t *testing.T) {
		f := newFixture(t, client, 5)
		beginning := serverTime(t).Add(time.Minute)
		setWindow(t, f, beginning, beginning.Add(time.Hour))
		keys := f.admission.keys(f.voucherID, f.candidate(0))
		before := dumpKeys(t, client, keys)
		_, err := f.admission.Reserve(ctx, f.voucherID, f.user(0), f.candidate(0), f.begin, f.begin, f.end, 1)
		if !errors.Is(err, ErrNotStarted) || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
			t.Fatalf("future sale accepted an in-window argument or mutated state: %v", err)
		}
	})
	t.Run("open_sale_uses_redis_time_for_admission_and_created_at", func(t *testing.T) {
		f := newFixture(t, client, 5)
		before := serverTime(t)
		setWindow(t, f, before, before.Add(time.Hour))
		stale := before.Add(-time.Hour)
		result, err := f.admission.Reserve(ctx, f.voucherID, f.user(0), f.candidate(0), stale, f.begin, f.end, 1)
		if err != nil || result.ID != f.candidate(0) || result.Reused {
			t.Fatalf("already-open sale rejected stale argument instead of using Redis time: %v", err)
		}
		created, err := client.HGet(ctx, "order:reservation:"+result.ID, "createdAt").Int64()
		after := serverTime(t)
		if err != nil || created < before.UnixMilli() || created > after.UnixMilli() || created == stale.UnixMilli() {
			t.Fatal("reservation creation time must be the actual Redis admission time")
		}
		f.requireCounts(t, 4, 1)
	})
	t.Run("exclusive_end_rejects_without_any_business_writes", func(t *testing.T) {
		f := newFixture(t, client, 5)
		ending := serverTime(t)
		setWindow(t, f, ending.Add(-time.Hour), ending)
		keys := f.admission.keys(f.voucherID, f.candidate(0))
		before := dumpKeys(t, client, keys)
		_, err := f.admission.Reserve(ctx, f.voucherID, f.user(0), f.candidate(0), f.end, f.begin, f.end, 1)
		if !errors.Is(err, ErrEnded) || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
			t.Fatalf("exclusive sales end allowed a new admission or mutated state: %v", err)
		}
	})
	t.Run("accepted_identity_still_recovers_after_actual_sales_end", func(t *testing.T) {
		f := newFixture(t, client, 5)
		first, err := f.reserve(t, 0)
		if err != nil {
			t.Fatal("cannot create original acceptance")
		}
		ending := serverTime(t).Add(-time.Second)
		setWindow(t, f, ending.Add(-time.Hour), ending)
		f.own(t, "order:reservation:"+f.candidate(1))
		keys := append(f.admission.keys(f.voucherID, f.candidate(1)), "order:reservation:"+first.ID)
		before := dumpKeys(t, client, keys)
		result, err := f.admission.Reserve(ctx, f.voucherID, f.user(0), f.candidate(1), f.end.Add(time.Hour), f.begin, f.end, 1)
		if err != nil || !result.Reused || result.ID != first.ID || !reflect.DeepEqual(before, dumpKeys(t, client, keys)) {
			t.Fatal("expired sale must recover the same accepted identity without another reservation")
		}
	})
}

func (f *fixture) requireCounts(t *testing.T, stock, admitted int64) {
	t.Helper()
	ctx := context.Background()
	if value, err := f.client.Get(ctx, "seckill:stock:"+f.voucherID).Int64(); err != nil || value != stock {
		t.Fatal("remaining inventory differs from unique accepted requests")
	}
	if value, err := f.client.SCard(ctx, "seckill:order:"+f.voucherID).Result(); err != nil || value != admitted {
		t.Fatal("purchase marker count differs from unique admissions")
	}
	if value, err := f.client.HLen(ctx, "seckill:accepted:"+f.voucherID).Result(); err != nil || value != admitted {
		t.Fatal("accepted mapping count differs from unique admissions")
	}
	if value, err := f.client.XLen(ctx, f.admission.stream).Result(); err != nil || value != admitted {
		t.Fatal("stream message count differs from unique admissions")
	}
	var reservations []string
	for key := range f.owned {
		if len(key) > 18 && key[:18] == "order:reservation:" {
			reservations = append(reservations, key)
		}
	}
	if value, err := f.client.Exists(ctx, reservations...).Result(); err != nil || value != admitted {
		t.Fatal("reservation count differs from unique admissions")
	}
}

func TestIntegrationOrderID(t *testing.T) {
	client := integrationRedis(t)
	ctx := context.Background()
	t.Run("concurrent_daily_ids_keep_legacy_shanghai_high_bits_and_no_ttl", func(t *testing.T) {
		f := newFixture(t, client, 1)
		now := time.Date(2026, 10, 26, 0, 10, 20, 0, time.UTC)
		key := f.admission.counterPrefix + now.In(businessZone).Format("2006:01:02")
		f.own(t, key)
		var wait sync.WaitGroup
		start := make(chan struct{})
		ids := make([]string, 20)
		failures := make([]error, 20)
		for index := 0; index < 20; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				ids[index], failures[index] = f.admission.NextID(ctx, now)
			}(index)
		}
		close(start)
		wait.Wait()
		wall := time.Date(2026, 10, 26, 8, 10, 20, 0, time.UTC).Unix() - 1640995200
		seen := map[uint64]bool{}
		for index, id := range ids {
			n, err := strconv.ParseUint(id, 10, 64)
			if failures[index] != nil || err != nil || n > math.MaxInt64 || n>>32 != uint64(wall) || n&math.MaxUint32 < 1 || n&math.MaxUint32 > 20 || seen[n] {
				t.Fatal("daily IDs lost precision, legacy high bits or uniqueness")
			}
			seen[n] = true
		}
		if count, err := client.Get(ctx, key).Int(); err != nil || count != 20 {
			t.Fatal("counter did not serialize concurrent allocation")
		}
		if ttl, err := client.TTL(ctx, key).Result(); err != nil || ttl != -1 {
			t.Fatal("order counter must not expire")
		}
	})
	t.Run("bad_counter_type_value_ttl_and_exhaustion_never_reset_or_increment", func(t *testing.T) {
		for _, value := range []string{"wrong-type", "-1", "01", "1.0", "4294967295", "4294967296", "ttl"} {
			t.Run(value, func(t *testing.T) {
				f := newFixture(t, client, 1)
				now := f.now
				key := f.admission.counterPrefix + now.Format("2006:01:02")
				f.own(t, key)
				if value == "wrong-type" {
					if client.LPush(ctx, key, "owned").Err() != nil {
						t.Fatal("fixture failed")
					}
				} else if value == "ttl" {
					if client.Set(ctx, key, "1", time.Minute).Err() != nil {
						t.Fatal("fixture failed")
					}
				} else if client.Set(ctx, key, value, 0).Err() != nil {
					t.Fatal("fixture failed")
				}
				before := dumpKeys(t, client, []string{key})
				if _, err := f.admission.NextID(ctx, now); !errors.Is(err, ErrUnsafe) || !reflect.DeepEqual(before, dumpKeys(t, client, []string{key})) {
					t.Fatal("unsafe sequence was incremented or silently reset")
				}
			})
		}
	})
	t.Run("last_32_bit_sequence_is_valid_then_exhaustion_fails_without_wrap", func(t *testing.T) {
		f := newFixture(t, client, 1)
		key := f.admission.counterPrefix + f.now.Format("2006:01:02")
		f.own(t, key)
		if client.Set(ctx, key, "4294967294", 0).Err() != nil {
			t.Fatal("fixture failed")
		}
		id, err := f.admission.NextID(ctx, f.now)
		n, parseErr := strconv.ParseUint(id, 10, 64)
		if err != nil || parseErr != nil || n&math.MaxUint32 != math.MaxUint32 || n > math.MaxInt64 {
			t.Fatal("last legal daily sequence did not produce a signed 64-bit ID")
		}
		if _, err := f.admission.NextID(ctx, f.now); !errors.Is(err, ErrUnsafe) {
			t.Fatal("daily sequence overflow was accepted")
		}
		if value, _ := client.Get(ctx, key).Result(); value != "4294967295" {
			t.Fatal("exhausted sequence changed after failure")
		}
	})
	t.Run("daily_rollover_uses_independent_counter_keys", func(t *testing.T) {
		f := newFixture(t, client, 1)
		before := time.Date(2026, 10, 26, 23, 59, 59, 0, businessZone)
		after := before.Add(time.Second)
		for _, now := range []time.Time{before, after} {
			f.own(t, f.admission.counterPrefix+now.Format("2006:01:02"))
		}
		first, err := f.admission.NextID(ctx, before)
		if err != nil {
			t.Fatal("first day allocation failed")
		}
		second, err := f.admission.NextID(ctx, after)
		if err != nil {
			t.Fatal("second day allocation failed")
		}
		a, _ := strconv.ParseUint(first, 10, 64)
		b, _ := strconv.ParseUint(second, 10, 64)
		if a&math.MaxUint32 != 1 || b&math.MaxUint32 != 1 || b-a != 1<<32 {
			t.Fatal("daily counter rollover changed legacy ID layout")
		}
	})
}

func TestIntegrationAcceptedRecovery(t *testing.T) {
	client := integrationRedis(t)
	ctx := context.Background()
	f := newFixture(t, client, 1)
	if id, err := f.admission.Recover(ctx, f.voucherID, f.user(0)); err != nil || id != "" {
		t.Fatal("missing accepted map must be an empty lookup")
	}
	key := "seckill:accepted:" + f.voucherID
	if client.Set(ctx, key, "wrong-type", 0).Err() != nil {
		t.Fatal("fixture failed")
	}
	if _, err := f.admission.Recover(ctx, f.voucherID, f.user(0)); !errors.Is(err, ErrUnsafe) {
		t.Fatal("wrong-type accepted lookup must not become an empty success")
	}
	if client.Del(ctx, key).Err() != nil || client.HSet(ctx, key, f.user(0), "9223372036854775808").Err() != nil {
		t.Fatal("fixture failed")
	}
	if _, err := f.admission.Recover(ctx, f.voucherID, f.user(0)); !errors.Is(err, ErrUnsafe) {
		t.Fatal("out-of-range accepted order ID was returned")
	}
	if client.HSet(ctx, key, f.user(0), fmt.Sprint(int64(math.MaxInt64))).Err() != nil {
		t.Fatal("fixture failed")
	}
	if id, err := f.admission.Recover(ctx, f.voucherID, f.user(0)); err != nil || id != "9223372036854775807" {
		t.Fatal("valid accepted lookup lost precision")
	}
}
