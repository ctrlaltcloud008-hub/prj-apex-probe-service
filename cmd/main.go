package main

import (
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/config"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/gcs"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/handler"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/pubsub"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/service"
	"github.com/ctrlaltcloud008-hub/prj-apex-probe-service/internal/spanner"

	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/logger"
	"github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/otel"
	pbclient "github.com/ctrlaltcloud008-hub/prj-apex-core-modules/pkg/pubsub"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func run() error {

	cfg, err := config.LoadProbeConfig()
	if err != nil {
		return err
	}

	logger := logger.New(cfg.Service(), cfg.Region(), cfg.AppEnv())

	logger.Info(
		context.Background(),
		"service.startup",
		"Ingestion service bootstrap started",
		slog.String("component", "bootstrap"),
		slog.String("http_addr", cfg.Port()),
		slog.String("project_id", cfg.ProjectID()),
		slog.String("spanner_database", cfg.SpannerDatabase()),
		slog.Bool("audit", false))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	trCfg := otel.TracerConfig{
		AppEnv:      cfg.AppEnv(),
		ServiceName: cfg.Service(),
		ProjectID:   cfg.ProjectID(),
		Region:      cfg.Region(),
	}

	shutdown, err := otel.InitTracer(ctx, trCfg)
	if err != nil {
		return err
	}

	logger.Info(
		ctx,
		"telemetry.initialized",
		"OpenTelemetry initialized",
		slog.String("component", "telemetry"),
		slog.String("project_id", cfg.ProjectID()),
		slog.Bool("audit", false),
	)

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(shutdownCtx); err != nil {
			logger.Error(
				context.Background(),
				"tracer.shutdown_failed",
				"Failed to shutdown tracer",
				slog.String("component", "telemetry"),
				slog.String("error", err.Error()),
				slog.Int("timeout_seconds", 5),
				slog.String("outcome", "failure"),
				slog.Bool("audit", false),
			)
			return
		}
		logger.Info(
			context.Background(),
			"tracer.shutdown_success",
			"Tracer shutdown completed",
			slog.String("component", "telemetry"),
			slog.Int("timeout_seconds", 5),
			slog.Bool("audit", false),
		)
	}()

	client, err := pubsub.NewClient(ctx, pubsub.Config{
		ProjectID:      cfg.ProjectID(),
		EnabledTracing: true,
	})

	if err != nil {
		return err
	}

	defer client.Close()

	logger.Info(
		ctx,
		"pubsub.client_initialized",
		"Pub/Sub client initialized",
		slog.String("component", "bootstrap"),
		slog.Bool("audit", false),
	)

	subscriber := pbclient.NewSubscriber(client, cfg.Subscription(),
		pbclient.WithMaxOutstandingMessages(100),
		pbclient.WithNumGoroutines(10),
		pbclient.WithMaxOutstandingBytes(100*1024*1024),
		// The probe handler enforces a 5-minute wall-clock limit per attempt
		// (full-object fallback included); keep the message lease alive for it.
		pbclient.WithMaxExtension(6*time.Minute),
	)

	spannerClient, err := spanner.NewClient(ctx, cfg.SpannerDatabase(), spanner.DefaultConfig())
	if err != nil {
		return err
	}
	defer spannerClient.Close()

	logger.Info(
		ctx,
		"spanner.client_initialized",
		"Spanner client initialized",
		slog.String("component", "bootstrap"),
		slog.String("spanner_database", cfg.SpannerDatabase()),
		slog.Bool("audit", false),
	)

	gcsClient, err := gcs.NewClient(ctx)
	if err != nil {
		return err
	}
	defer gcsClient.Close()

	logger.Info(
		ctx,
		"gcs.client_initialized",
		"GCS client initialized",
		slog.String("component", "bootstrap"),
		slog.Bool("audit", false),
	)

	service := service.NewMessageProcessor(logger, spannerClient, gcsClient)

	msgHandler := handler.NewHandler(logger, service, cfg.Region())

	logger.Info(
		ctx,
		"service.startup_complete",
		"All dependencies initialized, ready to receive messages",
		slog.String("component", "bootstrap"),
		slog.Bool("audit", false),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.Healthz)

	wrapped := otelhttp.NewHandler(mux, "ingestion-service.http")

	server := &http.Server{
		Addr:    cfg.Port(),
		Handler: wrapped,
	}

	logger.Info(
		ctx,
		"server.configured",
		"HTTP server configured",
		slog.String("component", "http_server"),
		slog.String("addr", cfg.Port()),
		slog.String("routes", "GET /healthz"),
		slog.Bool("audit", false),
	)

	handlerErrCh := make(chan error, 1)
	serverErrCh := make(chan error, 1)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info(
			ctx,
			"server.starting",
			"Starting HTTP server",
			slog.String("component", "http_server"),
			slog.String("addr", cfg.Port()),
			slog.Bool("audit", false),
		)
		if err := server.ListenAndServe(); err != nil {
			if err == http.ErrServerClosed {
				logger.Info(
					context.Background(),
					"server.stopped",
					"HTTP server stopped accepting new requests",
					slog.String("component", "http_server"),
					slog.String("addr", cfg.Port()),
					slog.Bool("audit", false),
				)
				return
			}
			serverErrCh <- err
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info(ctx,
			"subscriber.starting",
			"Starting Pub/Sub subscriber",
			slog.String("component", "subscriber"),
			slog.String("subscription", cfg.Subscription()),
			slog.Bool("audit", false))

		if err := subscriber.Receive(ctx, msgHandler.HandleMessage); err != nil {

			logger.Error(ctx,
				"subscriber.failed",
				"Pub/Sub subscriber failed",
				slog.String("component", "subscriber"),
				slog.String("subscription", cfg.Subscription()),
				slog.String("error", err.Error()),
				slog.String("outcome", "failure"),
				slog.Bool("audit", false),
			)
			handlerErrCh <- fmt.Errorf("subscriber: %w", err)
		}
	}()

	if cfg.DLQSubscription() != "" {
		dlqSubscriber := pbclient.NewSubscriber(client, cfg.DLQSubscription(),
			pbclient.WithMaxOutstandingMessages(10),
			pbclient.WithNumGoroutines(1),
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info(ctx,
				"subscriber.starting",
				"Starting DLQ subscriber",
				slog.String("component", "dlq_subscriber"),
				slog.String("subscription", cfg.DLQSubscription()),
				slog.Bool("audit", false))

			if err := dlqSubscriber.Receive(ctx, msgHandler.HandleDLQMessage); err != nil {
				logger.Error(ctx,
					"subscriber.failed",
					"DLQ subscriber failed",
					slog.String("component", "dlq_subscriber"),
					slog.String("subscription", cfg.DLQSubscription()),
					slog.String("error", err.Error()),
					slog.String("outcome", "failure"),
					slog.Bool("audit", false),
				)
				handlerErrCh <- fmt.Errorf("dlq subscriber: %w", err)
			}
		}()
	}

	var runErr error

	select {
	case <-ctx.Done():
		logger.Info(
			ctx,
			"shutdown.initiated",
			"Shutdown signal received",
			slog.String("component", "lifecycle"),
			slog.String("reason", "signal"),
			slog.Bool("audit", false),
		)
	case err := <-serverErrCh:
		runErr = err
		logger.Error(
			ctx,
			"server.failed",
			"HTTP server failed",
			slog.String("component", "http_server"),
			slog.String("addr", cfg.Port()),
			slog.String("error", err.Error()),
			slog.String("outcome", "failure"),
			slog.Bool("audit", false),
		)
	case err := <-handlerErrCh:
		runErr = err
		logger.Error(
			ctx,
			"handler.failed",
			"Message handler failed",
			slog.String("component", "message_handler"),
			slog.String("error", err.Error()),
			slog.String("outcome", "failure"),
			slog.Bool("audit", false),
		)
	}
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(
			ctx,
			"server.shutdown_failed",
			"Failed to shutdown HTTP server",
			slog.String("component", "http_server"),
			slog.String("error", err.Error()),
			slog.String("outcome", "failure"),
			slog.Bool("audit", false),
		)
		if runErr == nil {
			runErr = fmt.Errorf("http server shutdown: %w", err)
		}
	} else {
		logger.Info(
			ctx,
			"server.shutdown",
			"HTTP server shutdown complete",
			slog.String("component", "http_server"),
			slog.Bool("audit", false),
		)
	}

	wg.Wait()

	return runErr

}
