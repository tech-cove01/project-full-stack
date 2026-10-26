// Package app assembles the API and owns process resource lifetimes.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/activity"
	"github.com/cityloop/fullstack-go/internal/activitypub"
	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/merchant"
	"github.com/cityloop/fullstack-go/internal/order"
	"github.com/cityloop/fullstack-go/internal/orderstream"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
	"github.com/cityloop/fullstack-go/internal/shop"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger, revision string) error {
	startupCtx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	resources, err := database.Open(startupCtx, cfg)
	cancel()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.APIAddr)
	if err != nil {
		_ = resources.Close()
		return fmt.Errorf("listen HTTP API: %w", err)
	}
	server := &http.Server{
		Handler: httpapi.NewRouter(resources, cfg.ProbeTimeout, revision, logger, func(routes gin.IRouter) {
			identity := auth.NewService(resources.ORM, resources.Redis, cfg.Environment)
			auth.Register(routes, identity)
			shop.Register(routes, shop.NewService(resources.ORM, cfg.Location))
			merchant.Register(routes, merchant.NewService(resources.ORM), identity)
			activity.Register(routes, activity.NewService(resources.ORM, resources.Redis), identity)
			order.Register(routes, order.NewService(resources.ORM, resources.Redis, cfg.OrderPayTimeout), identity)
		}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	// Workers use process lifetime, never a request context. Persistent outbox
	// rows remain replayable when cancellation interrupts an in-flight event.
	owned := startBackground(ctx, resources,
		activitypub.NewWorker(resources.ORM, resources.Redis).Run,
		orderstream.NewWorker(resources.Redis, order.NewCreator(resources.ORM, cfg.OrderPayTimeout)).Run)
	logger.Info("Go API ready", "service", "project-full-stack-go", "runtime", "go", "version", revision, "address", listener.Addr().String())
	return serve(ctx, server, listener, owned, cfg.ShutdownTimeout, logger)
}

type backgroundResources struct {
	resources io.Closer
	cancel    context.CancelFunc
	done      <-chan struct{}
}

func startBackground(ctx context.Context, resources io.Closer, runs ...func(context.Context)) *backgroundResources {
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var workers sync.WaitGroup
	for _, run := range runs {
		workers.Add(1)
		go func() { defer workers.Done(); run(workerCtx) }()
	}
	go func() {
		defer close(done)
		workers.Wait()
	}()
	return &backgroundResources{resources: resources, cancel: cancel, done: done}
}

func (owned *backgroundResources) Close() error {
	owned.cancel()
	<-owned.done
	return owned.resources.Close()
}

// serve drains accepted HTTP work before releasing database/cache pools. The
// shutdown budget is independent of the cancelled process signal context.
func serve(ctx context.Context, server *http.Server, listener net.Listener, resources io.Closer, shutdownTimeout time.Duration, logger *slog.Logger) (returnErr error) {
	defer func() {
		if err := resources.Close(); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	serveFinished := false
	select {
	case err := <-serverErr:
		serveFinished = true
		if !errors.Is(err, http.ErrServerClosed) {
			returnErr = fmt.Errorf("serve HTTP API: %w", err)
		}
	case <-ctx.Done():
		logger.Info("Go API shutting down")
	}
	// Serve may stop accepting because of a listener failure while already
	// accepted handlers still use the pools. Drain on every exit path.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		returnErr = errors.Join(returnErr, errors.New("HTTP shutdown deadline exceeded; remaining connections closed"))
	}
	if !serveFinished {
		if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			returnErr = errors.Join(returnErr, fmt.Errorf("serve HTTP API: %w", err))
		}
	}
	if returnErr == nil {
		logger.Info("Go API HTTP requests drained")
	}
	return returnErr
}
