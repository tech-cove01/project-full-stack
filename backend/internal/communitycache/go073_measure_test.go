package communitycache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type measuredSQL struct {
	logger.Interface
	queries atomic.Int64
}

func (l *measuredSQL) Trace(ctx context.Context, start time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	if strings.Contains(strings.ToLower(sql), "from tb_blog") {
		l.queries.Add(1)
	}
	// The original logger is intentionally not called: SQL contains fixture IDs
	// and measurement output should stay machine-readable.
}

type measuredRedis struct{ commands atomic.Int64 }

func (h *measuredRedis) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *measuredRedis) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { h.commands.Add(1); return next(ctx, cmd) }
}
func (h *measuredRedis) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.commands.Add(int64(len(cmds)))
		return next(ctx, cmds)
	}
}

type sampleSet struct {
	Name          string    `json:"name"`
	SamplesMS     []float64 `json:"samples_ms"`
	DBQueries     int64     `json:"db_queries"`
	RedisCommands int64     `json:"redis_commands"`
	P50MS         float64   `json:"p50_ms"`
	P95MS         float64   `json:"p95_ms"`
	P99MS         float64   `json:"p99_ms"`
}

func (s *sampleSet) finish(sql *measuredSQL, red *measuredRedis) {
	s.DBQueries = sql.queries.Load()
	s.RedisCommands = red.commands.Load()
	values := append([]float64(nil), s.SamplesMS...)
	sort.Float64s(values)
	q := func(p int) float64 { return values[(len(values)*p+99)/100-1] }
	s.P50MS, s.P95MS, s.P99MS = q(50), q(95), q(99)
}

// Explicit opt-in and exact addresses prevent this measurement from touching a
// developer's normal database. It runs the production cache against real MySQL
// and Redis, not an HTTP endpoint or a simulated DB.
func TestGO073CacheMeasurement(t *testing.T) {
	if os.Getenv("GO073_CACHE_MEASURE") != "1" {
		t.Skip("explicit isolated measurement only")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MySQL.Host != "127.0.0.1" || cfg.MySQL.Port != 3400 || cfg.Redis.Host != "127.0.0.1" || cfg.Redis.Port != 6500 || cfg.MySQL.Database != "project_full_stack_go" {
		t.Fatal("refusing non-dedicated cache measurement environment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	sqlCount := &measuredSQL{Interface: logger.Default.LogMode(logger.Silent)}
	db := r.ORM.Session(&gorm.Session{Logger: sqlCount})
	redisCount := &measuredRedis{}
	r.Redis.AddHook(redisCount)
	ids := make([]uint64, 0, 32)
	defer func() {
		for _, id := range ids {
			if err := r.Redis.Del(context.Background(), key(id)).Err(); err != nil {
				t.Errorf("cleanup Redis %d: %v", id, err)
			}
			if err := r.ORM.Exec("DELETE FROM tb_blog WHERE id=? AND title='GO073 cache fixture'", id).Error; err != nil {
				t.Errorf("cleanup MySQL %d: %v", id, err)
			}
		}
		t.Logf("cleanup attempted for %d successfully inserted fixture IDs", len(ids))
	}()
	for i := 0; i < 32; i++ {
		id := 910730000 + uint64(i)
		if err := r.ORM.Exec("INSERT INTO tb_blog (id,shop_id,user_id,title,images,content,status,publish_version,liked,comments) VALUES (?,0,1,'GO073 cache fixture','','x','PUBLISHED',1,7,3)", id).Error; err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	cache := New(r.Redis, db)
	clearCounts := func() { sqlCount.queries.Store(0); redisCount.commands.Store(0) }
	reset := func() {
		for _, id := range ids {
			if err := cache.Invalidate(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	measure := func(s *sampleSet, fn func() error) {
		start := time.Now()
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		s.SamplesMS = append(s.SamplesMS, float64(time.Since(start).Nanoseconds())/1e6)
	}
	get := func(id uint64) error {
		v, err := cache.Get(ctx, id)
		if err == nil && (v.Likes != 7 || v.Comments != 3) {
			return fmt.Errorf("incorrect cache value: %+v", v)
		}
		return err
	}
	results := []sampleSet{}
	// The cold and warm phases use exactly one key and one sequential caller;
	// invalidation cost is excluded from the per-read latency.
	cold := sampleSet{Name: "cold_single"}
	clearCounts()
	for i := 0; i < 100; i++ {
		if err := cache.Invalidate(ctx, ids[0]); err != nil {
			t.Fatal(err)
		}
		measure(&cold, func() error { return get(ids[0]) })
	}
	cold.finish(sqlCount, redisCount)
	results = append(results, cold)
	hot := sampleSet{Name: "warm_l1_single"}
	clearCounts()
	for i := 0; i < 100; i++ {
		measure(&hot, func() error { return get(ids[0]) })
	}
	hot.finish(sqlCount, redisCount)
	results = append(results, hot)
	// Each burst starts from the same cold key, with a barrier so requests race.
	burst := sampleSet{Name: "cold_singleflight_32_callers"}
	clearCounts()
	for i := 0; i < 20; i++ {
		if err := cache.Invalidate(ctx, ids[0]); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 32)
		times := make(chan float64, 32)
		for j := 0; j < 32; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				at := time.Now()
				errs <- get(ids[0])
				times <- float64(time.Since(at).Nanoseconds()) / 1e6
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		close(times)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		for ms := range times {
			burst.SamplesMS = append(burst.SamplesMS, ms)
		}
	}
	burst.finish(sqlCount, redisCount)
	results = append(results, burst)
	batch := sampleSet{Name: "cold_batch_32_keys"}
	clearCounts()
	for i := 0; i < 20; i++ {
		reset()
		measure(&batch, func() error {
			v, err := cache.GetMany(ctx, ids)
			if err == nil && len(v) != len(ids) {
				return fmt.Errorf("batch returned %d", len(v))
			}
			return err
		})
	}
	batch.finish(sqlCount, redisCount)
	results = append(results, batch)
	individual := sampleSet{Name: "cold_sequential_32_keys"}
	clearCounts()
	for i := 0; i < 20; i++ {
		reset()
		measure(&individual, func() error {
			for _, id := range ids {
				if err := get(id); err != nil {
					return err
				}
			}
			return nil
		})
	}
	individual.finish(sqlCount, redisCount)
	results = append(results, individual)
	// Probe the implementation's cap directly; this is a local structural check,
	// not a DB workload or a claim about sustained HTTP throughput.
	local := New(r.Redis, db)
	max := 0
	for i := 0; i < 4100; i++ {
		local.writeLocal(uint64(i+1), Counts{})
		if len(local.local) > max {
			max = len(local.local)
		}
	}
	if max > 4096 || max != 4096 {
		t.Fatalf("local cache cap unexpected: %d", max)
	}
	if cold.DBQueries != 100 || hot.DBQueries != 0 || hot.RedisCommands != 0 || burst.DBQueries != 20 || batch.DBQueries != 20 || individual.DBQueries != 640 {
		t.Fatalf("unexpected DB or cache counts: %+v", results)
	}
	body, err := json.Marshal(map[string]any{"results": results, "local_max_entries": max, "local_entries_after_4100": len(local.local), "note_count": len(ids), "measurements": "in-process production cache, fixture invalidation excluded from sample latency but included in Redis command totals", "timestamp_utc": time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("GO073_CACHE_RESULT=%s\n", body)
}
