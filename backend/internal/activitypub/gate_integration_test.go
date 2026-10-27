package activitypub

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func integrationResources(t *testing.T, lock bool) *database.Resources {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated integration dependencies unavailable")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("cannot close integration dependencies")
		}
	})
	if lock {
		acquireIntegrationLock(t, resources.SQL)
	}
	return resources
}

func acquireIntegrationLock(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal("cannot reserve integration lock connection")
	}
	var acquired int
	if connection.QueryRowContext(ctx, "SELECT GET_LOCK('go021_outbox_integration', 30)").Scan(&acquired) != nil || acquired != 1 {
		_ = connection.Close()
		t.Fatal("another outbox integration owns the fixture window")
	}
	t.Cleanup(func() {
		var released int
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if connection.QueryRowContext(ctx, "SELECT RELEASE_LOCK('go021_outbox_integration')").Scan(&released) != nil || released != 1 {
			t.Error("cannot release integration lock")
		}
		_ = connection.Close()
	})
}

func randomID(t *testing.T) string {
	t.Helper()
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("cannot allocate fixture ID")
	}
	return strconv.FormatUint(9_007_199_254_740_993+binary.LittleEndian.Uint64(entropy[:])%1_000_000_000, 10)
}

func ownedGateID(t *testing.T, client *redis.Client) string {
	t.Helper()
	id := randomID(t)
	ctx := context.Background()
	if count, err := client.Exists(ctx, gateKeys(id)...).Result(); err != nil || count != 0 {
		t.Fatal("fixture gate keys already exist")
	}
	t.Cleanup(func() {
		if client.Del(ctx, gateKeys(id)...).Err() != nil {
			t.Error("cannot remove exact gate keys")
		}
		if count, err := client.Exists(ctx, gateKeys(id)...).Result(); err != nil || count != 0 {
			t.Error("gate cleanup left owned keys")
		}
	})
	return id
}

func TestIntegrationGate(t *testing.T) {
	resources := integrationResources(t, false)
	client, gate, ctx := resources.Redis, NewGate(resources.Redis), context.Background()
	begin := time.Date(2026, 10, 26, 8, 0, 0, 0, time.FixedZone("Shanghai", 8*3600))
	end := begin.Add(time.Hour)
	t.Run("every_wrong_key_type_is_rejected_without_partial_writes", func(t *testing.T) {
		for index := 0; index < 4; index++ {
			t.Run(strconv.Itoa(index), func(t *testing.T) {
				id := ownedGateID(t, client)
				keys := gateKeys(id)
				if client.LPush(ctx, keys[index], "owned wrong type").Err() != nil {
					t.Fatal("cannot arrange wrong key type")
				}
				before := keySnapshot(t, client, keys)
				if !errors.Is(gate.Prepare(ctx, id, 1, 10, begin, end), ErrUnsafe) {
					t.Fatal("prepare must reject every key with an unexpected type")
				}
				if !reflect.DeepEqual(before, keySnapshot(t, client, keys)) {
					t.Fatal("unsafe prepare performed a partial write")
				}
				if !errors.Is(gate.Open(ctx, id, 1), ErrUnsafe) {
					t.Fatal("open must reject incomplete or invalid preparation")
				}
				if !reflect.DeepEqual(before, keySnapshot(t, client, keys)) {
					t.Fatal("unsafe open performed a partial write")
				}
				if index == 0 && !errors.Is(gate.Close(ctx, id, 1), ErrUnsafe) {
					t.Fatal("close must reject a non-hash gate")
				}
			})
		}
	})
	t.Run("bad_script_arguments_and_aliased_keys_do_not_write", func(t *testing.T) {
		id := ownedGateID(t, client)
		keys := gateKeys(id)
		before := keySnapshot(t, client, keys)
		for _, arguments := range [][]any{
			{"bad", 10, begin.UnixMilli(), end.UnixMilli()}, {0, 10, begin.UnixMilli(), end.UnixMilli()},
			{1.5, 10, begin.UnixMilli(), end.UnixMilli()}, {1, -1, begin.UnixMilli(), end.UnixMilli()},
			{1, "NaN", begin.UnixMilli(), end.UnixMilli()}, {1, 10, "bad", end.UnixMilli()},
			{1, 10, begin.UnixMilli(), begin.UnixMilli()}, {1, 10, begin.UnixMilli(), "9007199254740992"},
		} {
			value, err := client.Eval(ctx, prepareSource, keys, arguments...).Int64()
			if err != nil || value != -2 || !reflect.DeepEqual(before, keySnapshot(t, client, keys)) {
				t.Fatal("bad Lua arguments wrote or raised after partially writing")
			}
		}
		aliased := []string{keys[0], keys[0], keys[2], keys[3]}
		if value, err := client.Eval(ctx, prepareSource, aliased, 1, 10, begin.UnixMilli(), end.UnixMilli()).Int64(); err != nil || value != -2 {
			t.Fatal("aliased keys must be rejected before writes")
		}
		for _, source := range []string{openSource, closeSource} {
			scriptKeys := keys
			if source == closeSource {
				scriptKeys = keys[:1]
			}
			for _, argument := range []any{"bad", 0, -1, 1.25, "2147483648"} {
				if value, err := client.Eval(ctx, source, scriptKeys, argument).Int64(); err != nil || value != -2 {
					t.Fatal("bad open/close version must fail safely")
				}
			}
		}
		if !reflect.DeepEqual(before, keySnapshot(t, client, keys)) {
			t.Fatal("bad Lua input left partial state")
		}
	})
	t.Run("retries_preserve_consumed_inventory_and_missing_keys_stay_closed", func(t *testing.T) {
		id := ownedGateID(t, client)
		if gate.Prepare(ctx, id, 1, 10, begin, end) != nil {
			t.Fatal("initial preparation failed")
		}
		if ready, err := gate.Ready(ctx, id, 1); err != nil || ready {
			t.Fatal("prepare must leave admission closed")
		}
		if gate.Open(ctx, id, 1) != nil || client.DecrBy(ctx, gateKeys(id)[1], 3).Err() != nil {
			t.Fatal("cannot consume prepared inventory")
		}
		if gate.Prepare(ctx, id, 1, 10, begin, end) != nil || gate.Open(ctx, id, 1) != nil {
			t.Fatal("same publication must be idempotent")
		}
		if remaining, err := client.Get(ctx, gateKeys(id)[1]).Int(); err != nil || remaining != 7 {
			t.Fatal("publication retry reset consumed inventory")
		}
		if gate.Close(ctx, id, 2) != nil || !errors.Is(gate.Open(ctx, id, 1), ErrStale) {
			t.Fatal("old OPEN crossed a newer offline gate")
		}
		if client.Del(ctx, gateKeys(id)[1]).Err() != nil {
			t.Fatal("cannot remove owned inventory key")
		}
		if !errors.Is(gate.Prepare(ctx, id, 3, 10, begin, end), ErrUnsafe) {
			t.Fatal("prepare must never rebuild missing inventory of an existing gate")
		}
		if count, err := client.Exists(ctx, gateKeys(id)[1]).Result(); err != nil || count != 0 {
			t.Fatal("missing inventory was silently regenerated")
		}
	})
	t.Run("same_version_offline_phase_blocks_failed_initialization_reopen", func(t *testing.T) {
		id := ownedGateID(t, client)
		// Prepare DB commit failed, then synchronous offline's DB commit failed.
		if gate.Prepare(ctx, id, 1, 10, begin, end) != nil || gate.Close(ctx, id, 1) != nil {
			t.Fatal("cannot arrange the two failed-commit window")
		}
		if !errors.Is(gate.Prepare(ctx, id, 1, 10, begin, end), ErrUnsafe) || !errors.Is(gate.Open(ctx, id, 1), ErrStale) {
			t.Fatal("same-version retry reopened an offline fence")
		}
		if gate.Close(ctx, id, 1) != nil {
			t.Fatal("repeating the same offline operation must be idempotent")
		}
		if ready, _ := gate.Ready(ctx, id, 1); ready {
			t.Fatal("offline phase became ready")
		}
	})
	t.Run("legacy_hashes_fail_closed_and_can_be_offlined", func(t *testing.T) {
		id := ownedGateID(t, client)
		keys := gateKeys(id)
		if client.HSet(ctx, keys[0], "state", "CLOSED", "version", "1").Err() != nil ||
			client.MSet(ctx, keys[1], "10", keys[2], begin.UnixMilli(), keys[3], end.UnixMilli()).Err() != nil {
			t.Fatal("cannot arrange legacy gate")
		}
		if !errors.Is(gate.Prepare(ctx, id, 1, 10, begin, end), ErrUnsafe) || !errors.Is(gate.Open(ctx, id, 1), ErrUnsafe) {
			t.Fatal("a legacy hash without phase cannot be opened automatically")
		}
		if gate.Close(ctx, id, 2) != nil {
			t.Fatal("a valid legacy gate must permit safe offline")
		}
	})
	t.Run("open_checks_corrupt_values_and_missing_projection_without_writes", func(t *testing.T) {
		for index := 1; index < 4; index++ {
			id := ownedGateID(t, client)
			keys := gateKeys(id)
			if gate.Prepare(ctx, id, 1, 10, begin, end) != nil || client.Set(ctx, keys[index], "not-an-integer", 0).Err() != nil {
				t.Fatal("cannot arrange corrupt projection")
			}
			before := keySnapshot(t, client, keys)
			if !errors.Is(gate.Open(ctx, id, 1), ErrUnsafe) || !reflect.DeepEqual(before, keySnapshot(t, client, keys)) {
				t.Fatal("corrupt projection was partially opened")
			}
			if client.Del(ctx, keys[index]).Err() != nil {
				t.Fatal("cannot remove projection")
			}
			if !errors.Is(gate.Open(ctx, id, 1), ErrUnsafe) {
				t.Fatal("a missing key cannot be opened")
			}
		}
	})
}

func keySnapshot(t *testing.T, client *redis.Client, keys []string) []string {
	t.Helper()
	result := make([]string, len(keys))
	for index, key := range keys {
		value, err := client.Dump(context.Background(), key).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			t.Fatal("cannot snapshot fixture keys")
		}
		result[index] = value
	}
	return result
}
