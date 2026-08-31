// Command api is the HTTP entry point.
//
// Boot order is: configuration, logging, connectors, router, server. Shutdown
// runs in reverse, and every connector opened here registers its close function
// with the shutdown registry so nothing is left to leak.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/database/postgres"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
	"github.com/MAbduhI/go-boilerplate/shared/shutdown"

	"github.com/MAbduhI/go-boilerplate/app/internal/router"
	// Enable a connector by uncommenting its import here and its block in
	// openConnectors below. Each one is already generated under shared/, so
	// there is nothing to write — only to switch on.
	//
	// redisclient "github.com/MAbduhI/go-boilerplate/shared/cache/redis"
	// "github.com/MAbduhI/go-boilerplate/shared/database/nosql/cassandra"
	// "github.com/MAbduhI/go-boilerplate/shared/database/nosql/dynamodb"
	// "github.com/MAbduhI/go-boilerplate/shared/database/nosql/mongo"
	// "github.com/MAbduhI/go-boilerplate/shared/mail/mailpit"
	// "github.com/MAbduhI/go-boilerplate/shared/messaging/kafka"
	// "github.com/MAbduhI/go-boilerplate/shared/messaging/temporal"
	// "github.com/MAbduhI/go-boilerplate/shared/search/elasticsearch"
)

func main() {
	// A separate run function means every deferred close still executes: a bare
	// os.Exit in main would skip them.
	if err := run(); err != nil {
		logger.L().Error("fatal", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	// LoadNearest walks up from the working directory, so the repository-root
	// .env is found whether the binary runs from the root or from app/. It never
	// overrides a real environment variable, so this is safe in a production
	// image, and a missing file is not an error.
	if err := config.LoadNearest(".env"); err != nil {
		return err
	}
	cfg := config.LoadApp()

	log := logger.Init(logger.Config{
		Level:  cfg.LogLevel,
		Format: cfg.LogFormat,
		Source: !cfg.IsProduction(),
	})
	log.Info("starting",
		slog.String("app", cfg.Name),
		slog.String("env", cfg.Env),
		slog.String("port", cfg.ServerPort))

	// signal.NotifyContext cancels rootCtx on SIGINT or SIGTERM. Kubernetes
	// sends SIGTERM before SIGKILL, so handling it is what makes a rolling
	// deploy drain instead of dropping in-flight requests.
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := shutdown.New(log)

	// bootCtx bounds connector startup so an unreachable dependency fails fast
	// rather than hanging the boot indefinitely.
	bootCtx, cancelBoot := context.WithTimeout(rootCtx, 30*time.Second)
	defer cancelBoot()

	deps, err := openConnectors(bootCtx, registry)
	if err != nil {
		// Close whatever did open before giving up.
		closeCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = registry.Close(closeCtx)
		return err
	}

	engine := router.New(cfg, deps)

	server := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: engine,
		// Without these a slow or malicious client can hold a connection open
		// indefinitely and exhaust the server.
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Serve in the background so the main goroutine can wait on either a signal
	// or a listener failure, whichever comes first.
	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", slog.String("addr", server.Addr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
			defer cancel()
			_ = registry.Close(closeCtx)
			return err
		}
	case <-rootCtx.Done():
		log.Info("shutdown signal received", slog.Duration("grace_period", cfg.ShutdownTimeout))
	}

	// Stop trapping signals so a second Ctrl-C aborts a hung shutdown instead of
	// being swallowed.
	stop()

	// The shutdown context is derived from Background, not rootCtx: rootCtx is
	// already cancelled by the signal, and draining needs a live deadline of its
	// own.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// Drain HTTP first: connectors must outlive the requests still using them.
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown did not complete cleanly", slog.String("error", err.Error()))
		// Force-close the listener and any remaining connections so the process
		// can still exit.
		_ = server.Close()
	} else {
		log.Info("http server drained")
	}

	log.Info("closing resources", slog.Any("order", registry.Names()))
	if err := registry.Close(shutdownCtx); err != nil {
		log.Error("some resources failed to close", slog.String("error", err.Error()))
		return err
	}

	log.Info("shutdown complete")
	return nil
}

// openConnectors opens every enabled connector and registers its close hook.
//
// Only Postgres is switched on. Each remaining block is complete and compiles as
// written once its import above is uncommented; the registry call beside it is
// what puts it in the shutdown path, so enabling a connector and closing it
// cleanly are the same edit.
func openConnectors(ctx context.Context, registry *shutdown.Registry) (*router.Dependencies, error) {
	deps := &router.Dependencies{}

	// ---- Postgres -----------------------------------------------------------
	db, err := postgres.New(ctx, postgres.LoadConfig())
	if err != nil {
		return nil, err
	}
	registry.RegisterErrFunc("postgres", db.Close)
	deps.DB = db

	// ---- Redis --------------------------------------------------------------
	// rdb, err := redisclient.New(ctx, redisclient.LoadConfig())
	// if err != nil {
	// 	return nil, err
	// }
	// registry.RegisterErrFunc("redis", rdb.Close)

	// ---- MongoDB ------------------------------------------------------------
	// mongoStore, err := mongo.New(ctx, mongo.LoadConfig())
	// if err != nil {
	// 	return nil, err
	// }
	// registry.Register("mongo", mongoStore.Close)

	// ---- Cassandra ----------------------------------------------------------
	// cassandraStore, err := cassandra.New(ctx, cassandra.LoadConfig())
	// if err != nil {
	// 	return nil, err
	// }
	// registry.Register("cassandra", cassandraStore.Close)

	// ---- DynamoDB -----------------------------------------------------------
	// dynamoStore, err := dynamodb.New(ctx, dynamodb.LoadConfig())
	// if err != nil {
	// 	return nil, err
	// }
	// registry.Register("dynamodb", dynamoStore.Close)

	// ---- Elasticsearch ------------------------------------------------------
	// esClient, err := elasticsearch.New(ctx, elasticsearch.LoadConfig())
	// if err != nil {
	// 	return nil, err
	// }
	// registry.RegisterErrFunc("elasticsearch", esClient.Close)

	// ---- Kafka --------------------------------------------------------------
	// producer := kafka.NewProducer(kafka.LoadConfig())
	// if err := producer.Ping(ctx); err != nil {
	// 	return nil, err
	// }
	// // Close flushes buffered records, so this hook is what prevents data loss
	// // on shutdown when KAFKA_ASYNC is enabled.
	// registry.RegisterErrFunc("kafka-producer", producer.Close)

	// ---- Temporal -----------------------------------------------------------
	// temporalClient, err := temporal.New(ctx, temporal.LoadConfig())
	// if err != nil {
	// 	return nil, err
	// }
	// registry.RegisterErrFunc("temporal", temporalClient.Close)
	//
	// // A worker, if this process runs one:
	// worker := temporalClient.NewWorker("")
	// // worker.RegisterWorkflow(...) / worker.RegisterActivity(...)
	// if err := worker.Start(); err != nil {
	// 	return nil, err
	// }
	// registry.RegisterFunc("temporal-worker", worker.Stop)

	// ---- Mailpit ------------------------------------------------------------
	// Mailpit holds no persistent connection, so it needs no close hook.
	// mailer := mailpit.New(mailpit.LoadConfig())
	// _ = mailer

	return deps, nil
}
