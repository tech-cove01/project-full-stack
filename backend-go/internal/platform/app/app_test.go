package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errAcceptFailed = errors.New("listener failed after accepting a request")

type failingListener struct {
	net.Listener
	fail    <-chan struct{}
	accepts int
}

func (listener *failingListener) Accept() (net.Conn, error) {
	listener.accepts++
	if listener.accepts > 1 {
		<-listener.fail
		return nil, errAcceptFailed
	}
	return listener.Listener.Accept()
}

type testResources struct {
	closed chan struct{}
	once   sync.Once
	calls  atomic.Int32
}

// Entry waits allow cold Windows process/network initialization without
// changing the shutdown budgets or the drain-ordering assertions below.
const requestStartTimeout = 5 * time.Second

func (resources *testResources) Close() error {
	resources.calls.Add(1)
	resources.once.Do(func() { close(resources.closed) })
	return nil
}

func TestShutdownDrainsAcceptedRequestBeforeClosingResources(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	resources := &testResources{closed: make(chan struct{})}
	started, finish := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish
		select {
		case <-resources.closed:
			t.Error("dependencies closed before accepted HTTP work finished")
		default:
		}
		_, _ = w.Write([]byte("completed"))
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() {
		stopped <- serve(ctx, server, listener, resources, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	clientDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			defer response.Body.Close()
			_, err = io.ReadAll(response.Body)
		}
		clientDone <- err
	}()
	select {
	case <-started:
	case err := <-clientDone:
		t.Fatalf("HTTP client completed before handler entry: %v", err)
	case <-time.After(requestStartTimeout):
		t.Fatal("request did not reach server")
	}
	cancel()
	select {
	case <-resources.closed:
		t.Fatal("shutdown did not allow in-flight HTTP request to drain")
	case <-time.After(30 * time.Millisecond):
	}
	close(finish)
	if err := <-clientDone; err != nil {
		t.Fatalf("draining request did not complete: %v", err)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("graceful shutdown did not finish")
	}
	if resources.calls.Load() != 1 {
		t.Fatalf("resource close calls = %d, expected 1", resources.calls.Load())
	}
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatal("HTTP listener remained open after shutdown")
	}
}

func TestShutdownDeadlineForcesCloseAndCancelsHandler(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	resources := &testResources{closed: make(chan struct{})}
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() {
		stopped <- serve(ctx, server, listener, resources, 30*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	clientDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
		clientDone <- err
	}()
	select {
	case <-started:
	case err := <-clientDone:
		t.Fatalf("HTTP client completed before handler entry: %v", err)
	case <-time.After(requestStartTimeout):
		t.Fatal("request did not reach server")
	}
	cancel()
	select {
	case err := <-stopped:
		if err == nil {
			t.Fatal("exceeded shutdown budget must be reported")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown deadline was not bounded")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("forced connection close did not cancel request context")
	}
	<-clientDone
	if resources.calls.Load() != 1 {
		t.Fatal("forced shutdown did not release dependencies exactly once")
	}
}

func TestUnexpectedListenerFailureDrainsActiveRequestBeforeClosingResources(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	failAccept := make(chan struct{})
	listener := &failingListener{Listener: base, fail: failAccept}
	resources := &testResources{closed: make(chan struct{})}
	started, finish := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish
		select {
		case <-resources.closed:
			t.Error("listener error closed resources before the active request finished")
		default:
		}
		_, _ = w.Write([]byte("completed"))
	})}
	stopped := make(chan error, 1)
	go func() {
		stopped <- serve(context.Background(), server, listener, resources, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	clientDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			defer response.Body.Close()
			_, err = io.ReadAll(response.Body)
		}
		clientDone <- err
	}()
	select {
	case <-started:
	case err := <-clientDone:
		t.Fatalf("HTTP client completed before handler entry: %v", err)
	case <-time.After(requestStartTimeout):
		t.Fatal("request did not reach server")
	}
	close(failAccept)
	select {
	case <-resources.closed:
		t.Fatal("unexpected Serve exit skipped HTTP draining")
	case <-time.After(30 * time.Millisecond):
	}
	close(finish)
	if err := <-clientDone; err != nil {
		t.Fatalf("accepted request was interrupted by listener failure: %v", err)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, errAcceptFailed) {
			t.Fatalf("expected listener failure to be preserved, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listener failure did not finish draining")
	}
	if resources.calls.Load() != 1 {
		t.Fatal("listener failure did not release dependencies exactly once")
	}
}
