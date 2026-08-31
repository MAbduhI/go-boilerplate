// Package temporal wraps the Temporal SDK client and worker.
//
// The SDK is fetched: Temporal's protocol, task polling and workflow determinism
// machinery are not something to reimplement. This wrapper only centralises
// configuration and shutdown.
package temporal

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/MAbduhI/go-boilerplate/shared/config"
	"github.com/MAbduhI/go-boilerplate/shared/logger"
)

// Config describes the Temporal connection and the worker.
type Config struct {
	HostPort  string
	Namespace string
	TaskQueue string
	Identity  string

	// Worker concurrency limits.
	MaxConcurrentActivityExecutionSize     int
	MaxConcurrentWorkflowTaskExecutionSize int

	ConnectTimeout time.Duration
}

// LoadConfig reads Config from TEMPORAL_* environment variables.
func LoadConfig() Config {
	return Config{
		HostPort:  config.String("TEMPORAL_HOST_PORT", "localhost:7233"),
		Namespace: config.String("TEMPORAL_NAMESPACE", "default"),
		TaskQueue: config.String("TEMPORAL_TASK_QUEUE", "default"),
		Identity:  config.String("TEMPORAL_IDENTITY", ""),

		MaxConcurrentActivityExecutionSize:     config.Int("TEMPORAL_MAX_CONCURRENT_ACTIVITIES", 100),
		MaxConcurrentWorkflowTaskExecutionSize: config.Int("TEMPORAL_MAX_CONCURRENT_WORKFLOW_TASKS", 100),

		ConnectTimeout: config.Duration("TEMPORAL_CONNECT_TIMEOUT", 10*time.Second),
	}
}

// Client wraps the Temporal client.
type Client struct {
	client client.Client
	cfg    Config
	log    *slog.Logger
}

// New dials Temporal.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
		defer cancel()
	}

	c, err := client.DialContext(ctx, client.Options{
		HostPort:  cfg.HostPort,
		Namespace: cfg.Namespace,
		Identity:  cfg.Identity,
		Logger:    &slogAdapter{log: logger.Module("temporal.sdk")},
	})
	if err != nil {
		return nil, fmt.Errorf("temporal: dial %s namespace %s: %w", cfg.HostPort, cfg.Namespace, err)
	}

	log := logger.Module("temporal")
	log.Info("connected",
		slog.String("host_port", cfg.HostPort),
		slog.String("namespace", cfg.Namespace),
		slog.String("task_queue", cfg.TaskQueue))

	return &Client{client: c, cfg: cfg, log: log}, nil
}

// Temporal exposes the SDK client for starting and signalling workflows.
func (c *Client) Temporal() client.Client { return c.client }

// Config reports the resolved configuration.
func (c *Client) Config() Config { return c.cfg }

// NewWorker builds a worker on the configured task queue. Register workflows and
// activities on the returned worker, then call Start.
//
//	w := tc.NewWorker("")
//	w.RegisterWorkflow(myWorkflow)
//	w.RegisterActivity(myActivity)
//	if err := w.Start(); err != nil { ... }
//	registry.RegisterFunc("temporal-worker", w.Stop)
func (c *Client) NewWorker(taskQueue string) worker.Worker {
	if taskQueue == "" {
		taskQueue = c.cfg.TaskQueue
	}
	return worker.New(c.client, taskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize:     c.cfg.MaxConcurrentActivityExecutionSize,
		MaxConcurrentWorkflowTaskExecutionSize: c.cfg.MaxConcurrentWorkflowTaskExecutionSize,
	})
}

// Ping verifies the connection by checking the namespace exists.
func (c *Client) Ping(ctx context.Context) error {
	if _, err := c.client.CheckHealth(ctx, &client.CheckHealthRequest{}); err != nil {
		return fmt.Errorf("temporal: health check: %w", err)
	}
	return nil
}

// Close releases the connection. Registered with the shutdown registry.
func (c *Client) Close() error {
	c.client.Close()
	return nil
}

// slogAdapter routes the SDK's logger into the shared structured logger, so
// Temporal's output does not arrive in a different format from everything else.
type slogAdapter struct{ log *slog.Logger }

func (a *slogAdapter) Debug(msg string, keyvals ...any) { a.log.Debug(msg, keyvals...) }
func (a *slogAdapter) Info(msg string, keyvals ...any)  { a.log.Info(msg, keyvals...) }
func (a *slogAdapter) Warn(msg string, keyvals ...any)  { a.log.Warn(msg, keyvals...) }
func (a *slogAdapter) Error(msg string, keyvals ...any) { a.log.Error(msg, keyvals...) }
