// Package diagnostics exposes opt-in, loopback-only process observations for
// isolated capacity experiments. It is never mounted on the product API mux.
package diagnostics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const maxProfileSeconds = 30

type Server struct {
	server  *http.Server
	done    chan error
	timeout time.Duration
	close   sync.Once
	err     error
}

type runtimeSnapshot struct {
	Goroutines    int     `json:"goroutines"`
	GOMAXPROCS    int     `json:"gomaxprocs"`
	HeapAlloc     uint64  `json:"heap_alloc_bytes"`
	HeapSys       uint64  `json:"heap_sys_bytes"`
	HeapInuse     uint64  `json:"heap_inuse_bytes"`
	TotalAlloc    uint64  `json:"total_alloc_bytes"`
	Mallocs       uint64  `json:"mallocs"`
	Frees         uint64  `json:"frees"`
	NumGC         uint32  `json:"num_gc"`
	PauseTotalNs  uint64  `json:"pause_total_ns"`
	LastGC        uint64  `json:"last_gc_unix_ns"`
	GCCPUFraction float64 `json:"gc_cpu_fraction"`
}

type sqlSnapshot struct {
	MaxOpenConnections int   `json:"max_open_connections"`
	OpenConnections    int   `json:"open_connections"`
	InUse              int   `json:"in_use"`
	Idle               int   `json:"idle"`
	WaitCount          int64 `json:"wait_count"`
	WaitDurationNS     int64 `json:"wait_duration_ns"`
	MaxIdleClosed      int64 `json:"max_idle_closed"`
	MaxIdleTimeClosed  int64 `json:"max_idle_time_closed"`
	MaxLifetimeClosed  int64 `json:"max_lifetime_closed"`
}

type redisSnapshot struct {
	Hits             uint32 `json:"hits"`
	Misses           uint32 `json:"misses"`
	Timeouts         uint32 `json:"timeouts"`
	WaitCount        uint32 `json:"wait_count"`
	WaitDurationNS   int64  `json:"wait_duration_ns"`
	Unusable         uint32 `json:"unusable"`
	TotalConnections uint32 `json:"total_connections"`
	IdleConnections  uint32 `json:"idle_connections"`
	StaleConnections uint32 `json:"stale_connections"`
}

// Start reads counters from the actual API pools without sending SQL or Redis
// commands. Port zero is supported by this lower-level constructor for tests;
// process configuration requires an explicit nonzero port.
func Start(address string, sqlPool *sql.DB, redisPool *redis.Client, timeout time.Duration) (*Server, error) {
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || sqlPool == nil || redisPool == nil || timeout <= 0 {
		return nil, errors.New("invalid isolated diagnostics configuration")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, errors.New("listen isolated diagnostics failed")
	}
	return serve(listener, handler(sqlPool.Stats, redisPool.PoolStats), timeout), nil
}

func serve(listener net.Listener, routes http.Handler, timeout time.Duration) *Server {
	server := &Server{
		server: &http.Server{Handler: routes, ReadHeaderTimeout: 2 * time.Second,
			ReadTimeout: 5 * time.Second, WriteTimeout: (maxProfileSeconds + 5) * time.Second,
			IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8 << 10},
		done: make(chan error, 1), timeout: timeout,
	}
	go func() { server.done <- server.server.Serve(listener) }()
	return server
}

// Close drains diagnostic requests before dependency pools are released. A
// cancelled or slow profiler cannot extend this close beyond the given budget.
func (server *Server) Close() error {
	server.close.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), server.timeout)
		defer cancel()
		if err := server.server.Shutdown(ctx); err != nil {
			_ = server.server.Close()
			server.err = errors.New("diagnostics shutdown deadline exceeded")
		}
		if err := <-server.done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			server.err = errors.Join(server.err, errors.New("diagnostics listener stopped unexpectedly"))
		}
	})
	return server.err
}

func handler(sqlStats func() sql.DBStats, redisStats func() *redis.PoolStats) http.Handler {
	started := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/capacity", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/debug/capacity" {
			http.NotFound(w, r)
			return
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		db, red := sqlStats(), redisStats()
		body := struct {
			At       string          `json:"at"`
			UptimeMS int64           `json:"uptime_ms"`
			PID      int             `json:"pid"`
			Runtime  runtimeSnapshot `json:"runtime"`
			SQL      sqlSnapshot     `json:"sql"`
			Redis    redisSnapshot   `json:"redis"`
		}{
			At: time.Now().UTC().Format(time.RFC3339Nano), UptimeMS: time.Since(started).Milliseconds(), PID: os.Getpid(),
			Runtime: runtimeSnapshot{runtime.NumGoroutine(), runtime.GOMAXPROCS(0), memory.HeapAlloc, memory.HeapSys, memory.HeapInuse,
				memory.TotalAlloc, memory.Mallocs, memory.Frees, memory.NumGC, memory.PauseTotalNs, memory.LastGC, memory.GCCPUFraction},
			SQL: sqlSnapshot{db.MaxOpenConnections, db.OpenConnections, db.InUse, db.Idle, db.WaitCount, int64(db.WaitDuration),
				db.MaxIdleClosed, db.MaxIdleTimeClosed, db.MaxLifetimeClosed},
			Redis: redisSnapshot{red.Hits, red.Misses, red.Timeouts, red.WaitCount, red.WaitDurationNs, red.Unusable, red.TotalConns, red.IdleConns, red.StaleConns},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	// Register only the required standard runtime profiles on this private mux.
	// cmdline, symbol, trace and arbitrary default-mux handlers are not exposed.
	mux.HandleFunc("/debug/pprof/{$}", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	for _, name := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.Handle("/debug/pprof/"+name, pprof.Handler(name))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Query().Get("gc") != "" {
			http.Error(w, "forced GC is not enabled", http.StatusBadRequest)
			return
		}
		if seconds := r.URL.Query().Get("seconds"); seconds != "" {
			value, err := strconv.Atoi(seconds)
			if err != nil || value < 1 || value > maxProfileSeconds {
				http.Error(w, "seconds must be between 1 and 30", http.StatusBadRequest)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
