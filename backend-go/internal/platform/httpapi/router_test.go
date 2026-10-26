package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type probeFunc func(context.Context) error

func (probe probeFunc) Check(ctx context.Context) error { return probe(ctx) }

func silentLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHealthSeparatesLivenessAndDependencyReadiness(t *testing.T) {
	var healthy atomic.Bool
	var calls atomic.Int32
	healthy.Store(true)
	router := NewRouter(probeFunc(func(context.Context) error {
		calls.Add(1)
		if !healthy.Load() {
			return errors.New("private DSN and password must not reach HTTP")
		}
		return nil
	}), time.Second, "test-revision", silentLogger())
	request := func(path string, expectedCode int, expectedStatus string) {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		var body health
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != expectedCode || body.Status != expectedStatus || body.Service != "project-full-stack-go" || body.Runtime != "go" || body.Version != "test-revision" {
			t.Fatalf("unexpected health response: code=%d body=%s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "password") {
			t.Fatal("readiness exposed dependency diagnostics")
		}
	}
	request("/actuator/health", http.StatusOK, "UP")
	healthy.Store(false)
	request("/actuator/health", http.StatusServiceUnavailable, "DOWN")
	request("/health/ready", http.StatusServiceUnavailable, "DOWN")
	before := calls.Load()
	request("/health/live", http.StatusOK, "UP")
	if calls.Load() != before {
		t.Fatal("liveness must not depend on MySQL/Redis availability")
	}
}

func TestReadinessHonorsRequestCancellationAndDeadline(t *testing.T) {
	router := NewRouter(probeFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}), 20*time.Millisecond, "", silentLogger())
	response := httptest.NewRecorder()
	started := time.Now()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable || time.Since(started) > time.Second {
		t.Fatal("readiness must return promptly when its dependency budget expires")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil).WithContext(ctx))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("cancelled readiness request must fail")
	}
}

func TestErrorsUseCompatibleJSONAndHidePanicValues(t *testing.T) {
	var logOutput strings.Builder
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	router := NewRouter(nil, time.Second, "", logger, func(routes gin.IRouter) {
		routes.GET("/panic", func(*gin.Context) { panic("private-password") })
	})
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/missing", http.StatusNotFound},
		{http.MethodPost, "/health/live", http.StatusMethodNotAllowed},
		{http.MethodGet, "/panic", http.StatusInternalServerError},
	} {
		t.Run(test.path+test.method, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			var body Result
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || body.Success || body.ErrorMsg == "" {
				t.Fatalf("unexpected error envelope: %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private-password") || strings.Contains(logOutput.String(), "private-password") {
				t.Fatal("recovered panic value must not be exposed")
			}
		})
	}
}
