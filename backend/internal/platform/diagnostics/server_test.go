package diagnostics

import (
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
)

func TestSnapshotUsesActualPoolCounterCallbacksAndOnlyExportsAllowlistedFields(t *testing.T) {
	var sqlCalls, redisCalls atomic.Int32
	routes := handler(func() sql.DBStats {
		sqlCalls.Add(1)
		return sql.DBStats{MaxOpenConnections: 20, OpenConnections: 12, InUse: 8, Idle: 4, WaitCount: 17, WaitDuration: 1500 * time.Millisecond}
	}, func() *redis.PoolStats {
		redisCalls.Add(1)
		return &redis.PoolStats{Hits: 25, Misses: 3, Timeouts: 1, WaitCount: 2, WaitDurationNs: 1000, TotalConns: 10, IdleConns: 7}
	})
	recorder := httptest.NewRecorder()
	routes.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/debug/capacity", nil))
	var body struct {
		PID     int             `json:"pid"`
		Runtime runtimeSnapshot `json:"runtime"`
		SQL     sqlSnapshot     `json:"sql"`
		Redis   redisSnapshot   `json:"redis"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 200 || body.PID <= 0 || body.Runtime.Goroutines == 0 || body.Runtime.HeapAlloc == 0 ||
		body.SQL.WaitCount != 17 || body.SQL.WaitDurationNS != int64(1500*time.Millisecond) || body.SQL.InUse != 8 ||
		body.Redis.Hits != 25 || body.Redis.TotalConnections != 10 || body.Redis.WaitCount != 2 || body.Redis.WaitDurationNS != 1000 || sqlCalls.Load() != 1 || redisCalls.Load() != 1 {
		t.Fatal("process or actual-pool counters were not sampled once")
	}
	for _, forbidden := range []string{"password", "query", "dsn", "address", "username"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatal("unexpected diagnostic field")
		}
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("observations must not be cached")
	}
}

func TestFeedTimingsAreDisabledByDefaultAndOnlyExposedOnPrivateSnapshot(t *testing.T) {
	for _, recorder := range []*taildiag.Recorder{nil, taildiag.New()} {
		routes := handler(func() sql.DBStats { return sql.DBStats{} }, func() *redis.PoolStats { return &redis.PoolStats{} }, recorder)
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/capacity", nil))
		var body struct {
			Tail taildiag.Snapshot `json:"tail"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Tail.Enabled != (recorder != nil) {
			t.Fatal("private snapshot did not preserve explicit observation configuration")
		}
		if recorder == nil && (len(body.Tail.Stages) != 0 || len(body.Tail.SlowTraces) != 0) {
			t.Fatal("disabled timing must not report made-up measurements")
		}
	}
}

func TestPrivateMuxRejectsMutationExpensiveDurationsAndOtherPprofSurfaces(t *testing.T) {
	routes := handler(func() sql.DBStats { return sql.DBStats{} }, func() *redis.PoolStats { return &redis.PoolStats{} })
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, "/debug/capacity", 405},
		{http.MethodHead, "/debug/capacity", 405},
		{http.MethodGet, "/debug/pprof/cmdline", 404},
		{http.MethodGet, "/debug/pprof/trace", 404},
		{http.MethodGet, "/debug/pprof/symbol", 404},
		{http.MethodGet, "/debug/pprof/profile?seconds=31", 400},
		{http.MethodGet, "/debug/pprof/profile?seconds=0", 400},
		{http.MethodGet, "/debug/pprof/heap?gc=1", 400},
		{http.MethodGet, "/health/ready", 404},
		{http.MethodGet, "/debug/pprof/heap", 200},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			routes.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != test.status {
				t.Fatalf("status=%d, want %d", recorder.Code, test.status)
			}
		})
	}
}

func TestBoundedCloseCancelsActiveDiagnosticRequestAndReleasesListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	requestFinished := make(chan struct{})
	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(requestFinished)
	})
	server := serve(listener, routes, 30*time.Millisecond)
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String() + "/active")
		if err == nil {
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, response.Body)
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach handler")
	}
	started := time.Now()
	if err := server.Close(); err == nil {
		t.Fatal("forced close must report the exceeded budget")
	}
	if time.Since(started) > time.Second {
		t.Fatal("diagnostics close exceeded its budget")
	}
	select {
	case <-requestFinished:
	case <-time.After(time.Second):
		t.Fatal("forced close did not cancel request")
	}
	<-clientDone
	if err := server.Close(); err == nil {
		t.Fatal("idempotent close lost the original error")
	}
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatal("diagnostic listener remained open")
	}
}

func TestGracefulCloseHasNoListenerLeak(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := serve(listener, http.NotFoundHandler(), time.Second)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartRejectsNonLoopbackAndMissingPoolsBeforeListening(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8350", "localhost:8350", "192.0.2.1:8350", ":8350", "bad"} {
		if _, err := Start(address, nil, nil, time.Second); err == nil {
			t.Fatal("unsafe diagnostic startup accepted")
		}
	}
	if _, err := Start("127.0.0.1:0", nil, nil, time.Second); err == nil {
		t.Fatal("missing actual pools accepted")
	}
}
