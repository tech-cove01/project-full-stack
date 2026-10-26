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
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
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
		Handler:           httpapi.NewRouter(resources, cfg.ProbeTimeout, revision, logger),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	logger.Info("Go API ready", "service", "project-full-stack-go", "runtime", "go", "version", revision, "address", listener.Addr().String())
	return serve(ctx, server, listener, resources, cfg.ShutdownTimeout, logger)
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
