package communitycards

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
)

// This opt-in scenario is wiring evidence, not a capacity run. The barrier is
// before the owned content SQL reaches the driver; no MySQL server wait is
// inferred from it. Production loader limits/deadlines are not increased.
func TestIntegrationCardsControlledSharedObservation(t *testing.T) {
	evidence := os.Getenv("CAP08_CONTROL_EVIDENCE")
	if os.Getenv("GO_INTEGRATION") != "1" || evidence == "" {
		t.Skip("single controlled scenario requires GO_INTEGRATION=1 and new CAP08_CONTROL_EVIDENCE")
	}
	if err := os.Mkdir(evidence, 0700); err != nil {
		t.Fatal("controlled evidence directory must be new with an existing parent")
	}
	f := integrationCards(t)
	row := struct {
		ID, ShopID, UserID uint64
		Title, Content     string
		Status             string
		PublishVersion     uint64
	}{UserID: 1, Title: "第二公共卡片", Content: "第二完整正文", Status: "PUBLISHED"}
	if err := f.r.ORM.Table("tb_blog").Create(&row).Error; err != nil || row.ID == 0 {
		t.Fatal("second owned content row setup failed")
	}
	secondRef := Ref{ID: row.ID, Version: 0}
	var cache *Cache
	t.Cleanup(func() {
		if cache != nil && cache.Close() != nil {
			t.Error("controlled cache cleanup failed")
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if f.r.Redis.Del(cleanup, key(secondRef)).Err() != nil {
			t.Error("second owned content key cleanup failed")
		}
		if f.r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_blog WHERE id=?", secondRef.ID).Error != nil {
			t.Error("second owned content row cleanup failed")
		}
		if count, err := f.r.Redis.Exists(cleanup, key(secondRef)).Result(); err != nil || count != 0 {
			t.Error("second owned content key survived cleanup")
		}
	})
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: f.r.SQL, SkipInitializeWithVersion: true}),
		&gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("controlled isolated ORM setup failed")
	}
	reached, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	var released atomic.Bool
	releaseSQL := func() { releaseOnce.Do(func() { released.Store(true); close(release) }) }
	defer releaseSQL()
	if db.Callback().Row().Before("gorm:row").Register("cap08_owned_content_barrier", func(tx *gorm.DB) {
		if !strings.Contains(tx.Statement.SQL.String(), "SELECT id,publish_version,title,content,images FROM tb_blog") ||
			len(tx.Statement.Vars) != 1 || tx.Statement.Vars[0] != f.ref.ID {
			return
		}
		enterOnce.Do(func() {
			close(reached)
			select {
			case <-release:
			case <-tx.Statement.Context.Done():
			}
		})
	}) != nil {
		t.Fatal("controlled content barrier registration failed")
	}
	recorder := taildiag.New()
	taildiag.Instrument(db, f.r.SQL, f.r.Redis)
	cfg := defaults()
	cfg.active = 1 // Test only: distinguish one active content load and its queue.
	cache = newCache(f.ctx, storage{redis: f.r.Redis, db: db}, cfg).WithDiagnostics(recorder)
	f.caches = append(f.caches, cache)
	// Direct callers carry synthetic service observations. This is not HTTP Feed
	// traffic; the real HTTP boundary remains covered by the existing FC tests.
	firstCtx, cancelFirst := context.WithCancel(f.ctx)
	defer cancelFirst()
	firstCtx, finishFirst := taildiag.Start(firstCtx, recorder, false)
	secondCtx, finishSecond := taildiag.Start(f.ctx, recorder, false)
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := cache.GetMany(firstCtx, []Ref{f.ref}); firstDone <- err }()
	await(t, reached)
	go func() { _, err := cache.GetMany(secondCtx, []Ref{secondRef}); secondDone <- err }()
	waitObservation(t, recorder, controlledStagesActive)
	cancelFirst()
	if err := await(t, firstDone); !errors.Is(err, context.Canceled) {
		t.Fatal("first direct caller did not cancel independently")
	}
	finishFirst(true)
	before := waitObservation(t, recorder, func(snapshot taildiag.Snapshot) bool {
		return controlledStagesActive(snapshot) && snapshot.Stages[taildiag.CardsMany].Active == 1 &&
			snapshot.Stages[taildiag.CardsColdSQL].OldestComplete && snapshot.Stages[taildiag.CardsSlotWait].OldestComplete &&
			snapshot.Stages[taildiag.CardsColdSQL].OldestActiveNS >= int64(taildiag.SlowThreshold) &&
			snapshot.Stages[taildiag.CardsSlotWait].OldestActiveNS >= int64(taildiag.SlowThreshold)
	})
	writeControlledJSON(t, filepath.Join(evidence, "before-release.json"), map[string]any{
		"at": time.Now().UTC().Format(time.RFC3339Nano), "barrierReleased": released.Load(),
		"firstCallerCanceled": true, "testActiveLimit": cfg.active, "productionActiveLimit": defaults().active,
		"loaderDeadlineMs": cfg.loadTimeout.Milliseconds(), "snapshot": before,
		"scope": "owned GORM before-Row barrier, not MySQL server execution or a capacity run",
	})
	var profileObserved atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/capacity", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sql, redis := f.r.SQL.Stats(), f.r.Redis.PoolStats()
		_ = json.NewEncoder(w).Encode(map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano),
			"pid": os.Getpid(), "tail": recorder.Snapshot(), "cards": cache.Stats(),
			"sql":   map[string]any{"in_use": sql.InUse, "wait_count": sql.WaitCount, "wait_duration_ns": int64(sql.WaitDuration)},
			"redis": map[string]any{"wait_count": redis.WaitCount, "wait_duration_ns": redis.WaitDurationNs}})
	})
	mux.HandleFunc("/debug/pprof/goroutine", func(w http.ResponseWriter, _ *http.Request) {
		if released.Load() || !controlledStagesActive(recorder.Snapshot()) {
			http.Error(w, "controlled stages no longer active", http.StatusConflict)
			return
		}
		profile := &boundedCardProfile{}
		if err := pprof.Lookup("goroutine").WriteTo(profile, 1); err != nil || profile.truncated || !controlledStagesActive(recorder.Snapshot()) {
			http.Error(w, "bounded controlled profile unavailable", http.StatusServiceUnavailable)
			return
		}
		profileObserved.Store(true)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(profile.body)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("controlled collector location unavailable")
	}
	collector := filepath.Join(filepath.Dir(source), "..", "..", "..", "scripts", "cap08-collector.py")
	python, err := exec.LookPath("python")
	if err != nil {
		t.Fatal("controlled collector requires the configured Python interpreter")
	}
	command := exec.CommandContext(f.ctx, python, "-B", "-X", "utf8", collector,
		"--url", server.URL+"/debug/capacity", "--output", filepath.Join(evidence, "collector"),
		"--expected-pid", strconv.Itoa(os.Getpid()), "--max-seconds", "2", "--max-captures", "1", "--stop-after-capture")
	output, collectorErr := command.CombinedOutput()
	writeControlledJSON(t, filepath.Join(evidence, "collector-exit.json"), map[string]any{
		"at": time.Now().UTC().Format(time.RFC3339Nano), "success": collectorErr == nil,
		"profileObservedBeforeRelease": profileObserved.Load(), "barrierReleased": released.Load(), "output": string(output),
	})
	if collectorErr != nil || !profileObserved.Load() || released.Load() || !controlledStagesActive(recorder.Snapshot()) {
		t.Fatal("collector did not capture the distinguishable SQL/queue stages before release")
	}
	raw, err := os.ReadFile(filepath.Join(evidence, "collector", "manifest.json"))
	var manifest struct {
		Captures []struct {
			Available, Truncated bool
			Reasons              []struct{ Kind, Name string }
		}
		PollCoverageComplete      bool `json:"poll_coverage_complete"`
		StageFieldsValid          bool `json:"stage_fields_valid"`
		ActiveAgeCoverageComplete bool `json:"active_age_coverage_complete"`
	}
	if err != nil || json.Unmarshal(raw, &manifest) != nil || len(manifest.Captures) != 1 || !manifest.Captures[0].Available || manifest.Captures[0].Truncated ||
		!manifest.PollCoverageComplete || !manifest.StageFieldsValid || !manifest.ActiveAgeCoverageComplete {
		t.Fatal("controlled collector did not preserve both pre-release age triggers and one bounded profile")
	}
	ageReasons := make(map[string]bool)
	for _, reason := range manifest.Captures[0].Reasons {
		if reason.Kind == "stage_active_age" {
			ageReasons[reason.Name] = true
		}
	}
	if !ageReasons["cards.cold_sql"] || !ageReasons["cards.slot_wait"] {
		t.Fatal("controlled profile was not triggered by the independently observed content SQL and slot queue")
	}
	releaseSQL()
	if err := await(t, secondDone); err != nil {
		t.Fatal("queued caller did not return after SQL release")
	}
	finishSecond(false)
	if cache.Close() != nil {
		t.Fatal("controlled cache close failed")
	}
	writeControlledJSON(t, filepath.Join(evidence, "after-release.json"), map[string]any{
		"at": time.Now().UTC().Format(time.RFC3339Nano), "barrierReleased": released.Load(), "snapshot": recorder.Snapshot(), "cards": cache.Stats(),
	})
	t.Log("controlled SQL and slot queue were separately observed and captured before release; first caller cancellation left shared work active")
}

func controlledStagesActive(snapshot taildiag.Snapshot) bool {
	return snapshot.Stages[taildiag.CardsShared].Active == 2 && snapshot.Stages[taildiag.CardsColdSQL].Active == 1 && snapshot.Stages[taildiag.CardsSlotWait].Active == 1
}

func writeControlledJSON(t *testing.T, path string, value any) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("controlled output must be new")
	}
	if err = json.NewEncoder(file).Encode(value); err != nil {
		_ = file.Close()
		t.Fatal("controlled output write failed")
	}
	if err = file.Close(); err != nil {
		t.Fatal("controlled output close failed")
	}
}

type boundedCardProfile struct {
	body      []byte
	truncated bool
}

func (profile *boundedCardProfile) Write(value []byte) (int, error) {
	const maximum = 1 << 20
	remaining := maximum - len(profile.body)
	if len(value) > remaining {
		profile.body = append(profile.body, value[:remaining]...)
		profile.truncated = true
	} else {
		profile.body = append(profile.body, value...)
	}
	return len(value), nil
}
