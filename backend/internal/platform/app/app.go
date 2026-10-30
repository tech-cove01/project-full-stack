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
	"github.com/cityloop/fullstack-go/internal/aichat"
	"github.com/cityloop/fullstack-go/internal/aiknowledge"
	"github.com/cityloop/fullstack-go/internal/aitool"
	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/communitycache"
	"github.com/cityloop/fullstack-go/internal/communitycards"
	"github.com/cityloop/fullstack-go/internal/communityconsumer"
	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"github.com/cityloop/fullstack-go/internal/communityrelay"
	"github.com/cityloop/fullstack-go/internal/merchant"
	"github.com/cityloop/fullstack-go/internal/merchantorder"
	"github.com/cityloop/fullstack-go/internal/note"
	"github.com/cityloop/fullstack-go/internal/notesearch"
	"github.com/cityloop/fullstack-go/internal/order"
	"github.com/cityloop/fullstack-go/internal/ordercomp"
	"github.com/cityloop/fullstack-go/internal/orderreview"
	"github.com/cityloop/fullstack-go/internal/orderstream"
	"github.com/cityloop/fullstack-go/internal/ordertimeout"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/diagnostics"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
	"github.com/cityloop/fullstack-go/internal/shop"
	"github.com/cityloop/fullstack-go/internal/social"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger, revision string) error {
	startupCtx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	resources, err := database.Open(startupCtx, cfg)
	cancel()
	if err != nil {
		return err
	}
	cards := communitycards.New(ctx, resources.Redis, resources.ORM)
	owned := applicationResources{cards: cards, pools: resources}
	var feedObservations *taildiag.Recorder
	if cfg.FeedDiagnostics {
		feedObservations = taildiag.New()
		taildiag.Instrument(resources.ORM, resources.SQL, resources.Redis)
	}
	listener, err := net.Listen("tcp", cfg.APIAddr)
	if err != nil {
		_ = owned.Close()
		return fmt.Errorf("listen HTTP API: %w", err)
	}
	orders := order.NewService(resources.ORM, resources.Redis, cfg.OrderPayTimeout)
	searchClient := configuredSearch(cfg.Search)
	if searchClient != nil {
		defer searchClient.CloseIdleConnections()
	}
	noteSearch := notesearch.NewService(resources.ORM, searchClient)
	// One provider instance is shared so indexing and retrieval always speak the
	// same model; a model switch requires rebuilding the chunk vectors.
	embedder, capabilities := configuredAI(cfg.AI)
	if pool, ok := embedder.(interface{ CloseIdleConnections() }); ok {
		defer pool.CloseIdleConnections()
	}
	knowledge := aiknowledge.NewService(resources.ORM, embedder).WithRetrieval(cfg.AI.TopK, cfg.AI.MinScore)
	conversations := aichat.NewConversations(resources.Redis)
	server := &http.Server{
		Handler: httpapi.NewRouter(resources, cfg.ProbeTimeout, revision, logger, func(routes gin.IRouter) {
			identity := auth.NewService(resources.ORM, resources.Redis, cfg.Environment)
			auth.Register(routes, identity)
			shop.Register(routes, shop.NewService(resources.ORM, cfg.Location))
			merchant.Register(routes, merchant.NewService(resources.ORM), identity)
			merchantorder.Register(routes, merchantorder.NewService(resources.ORM), identity)
			aiknowledge.Register(routes, knowledge, identity)
			note.Register(routes, note.NewService(resources.ORM), identity)
			notesearch.Register(routes, noteSearch)
			social.Register(routes,
				social.NewService(resources.ORM).
					WithProjections().
					WithDiagnostics(feedObservations).
					WithCards(cards).
					WithCounts(communitycache.New(resources.Redis, resources.ORM)).
					WithLikes(communitycache.NewLikes(resources.Redis)), identity)
			var assistant *aichat.Assistant
			if capabilities.Ready {
				assistant = aichat.NewAssistant(conversations, knowledge, aitool.NewService(resources.ORM), embedder).
					WithGuards(aichat.NewDedupe(resources.Redis), aichat.NewLock(resources.Redis)).
					WithBudget(aichat.NewBudget(resources.Redis)).WithRetrieval(cfg.AI.TopK, cfg.AI.MinScore)
			}
			aichat.Register(routes, conversations, assistant, capabilities.Mode, identity, capabilities)
			activity.Register(routes, activity.NewService(resources.ORM, resources.Redis), identity)
			order.Register(routes, orders, identity)
		}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	// Workers use process lifetime, never a request context. Persistent outbox
	// rows remain replayable when cancellation interrupts an in-flight event.
	workers := []func(context.Context){
		activitypub.NewWorker(resources.ORM, resources.Redis).Run,
		ordertimeout.NewWorker(resources.ORM, orders).Run,
		ordercomp.NewWorker(resources.ORM, resources.Redis).Run,
	}
	// Creator and review repository hold only immutable configuration and safe
	// database pools. Every consumer needs its own identity, cursor and retries.
	// Durable order/review evidence is still required before each entry's ACK.
	creator := order.NewCreator(resources.ORM, cfg.OrderPayTimeout)
	reviewer := orderreview.NewRepository(resources.ORM)
	for range cfg.OrderStreamWorkers {
		stream := orderstream.NewWorker(resources.Redis, creator)
		stream.UseReviewer(reviewer)
		workers = append(workers, stream.Run)
	}
	if len(cfg.Kafka.Brokers) > 0 {
		// The community relay only runs when a broker is configured; local
		// development stays a two-container environment.
		relay := communityrelay.NewWorker(resources.ORM, cfg.Kafka.Brokers, cfg.Kafka.Topic)
		defer func() { _ = relay.Close() }()
		workers = append(workers, relay.Run)
		projection := communityprojection.NewRuntime(communityprojection.New(resources.ORM))
		for _, name := range []string{communityprojection.Follow, communityprojection.Feed} {
			consumer, err := communityconsumer.NewWorker(cfg.Kafka.Brokers, cfg.Kafka.Topic, name, projection)
			if err != nil {
				_ = listener.Close()
				_ = owned.Close()
				return errors.New("community consumer configuration is invalid")
			}
			// Run closes the Kafka reader before startBackground releases DB pools.
			// Defer also covers assembly failure before background work starts.
			defer func() { _ = consumer.Close() }()
			workers = append(workers, consumer.Run)
		}
		if searchClient != nil {
			consumer, err := communityconsumer.NewWorker(cfg.Kafka.Brokers, cfg.Kafka.Topic, notesearch.Projection, notesearch.NewSync(resources.ORM, searchClient))
			if err != nil {
				_ = listener.Close()
				_ = owned.Close()
				return errors.New("note search consumer configuration is invalid")
			}
			defer func() { _ = consumer.Close() }()
			workers = append(workers, consumer.Run)
		}
	}
	if cfg.DiagnosticsAddr != "" {
		observation, err := diagnostics.StartWithCards(cfg.DiagnosticsAddr, resources.SQL, resources.Redis, cfg.ShutdownTimeout, cards, feedObservations)
		if err != nil {
			_ = listener.Close()
			_ = owned.Close()
			return err
		}
		owned.diagnostics = observation
		logger.Info("isolated capacity diagnostics enabled", "address", cfg.DiagnosticsAddr)
	}
	background := startBackground(ctx, owned, workers...)
	logger.Info("Go API ready", "service", "project-full-stack-go", "runtime", "go", "version", revision, "address", listener.Addr().String(), "order_stream_workers", cfg.OrderStreamWorkers)
	return serve(ctx, server, listener, background, cfg.ShutdownTimeout, logger)
}

type applicationResources struct {
	diagnostics io.Closer
	cards       io.Closer
	pools       io.Closer
}

func (resources applicationResources) Close() error {
	var err error
	if resources.diagnostics != nil {
		err = resources.diagnostics.Close()
	}
	if resources.cards != nil {
		err = errors.Join(err, resources.cards.Close())
	}
	return errors.Join(err, resources.pools.Close())
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
