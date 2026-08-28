package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"kitchen-api/internal/establishment/config"
	"kitchen-api/internal/establishment/kitchenapi"
	"kitchen-api/internal/establishment/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("establishment stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	client, err := kitchenapi.NewClientWithResponses(
		cfg.KitchenBaseURL,
		kitchenapi.WithHTTPClient(&http.Client{Timeout: cfg.RequestTimeout}),
		kitchenapi.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
			request.Header.Set("Authorization", "Bearer "+cfg.PartnerAPIKey)
			return nil
		}),
	)
	if err != nil {
		return err
	}

	processor := worker.NewOrderProcessor(client, cfg.ProcessInterval, logger)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		processor.Run(runCtx)
	}()

	logger.Info(
		"establishment started",
		"kitchen_base_url",
		cfg.KitchenBaseURL,
		"process_interval",
		cfg.ProcessInterval,
	)

	<-ctx.Done()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		cfg.ShutdownTimeout,
	)
	defer shutdownCancel()

	select {
	case <-done:
		return nil
	case <-shutdownCtx.Done():
		return fmt.Errorf("shutdown establishment worker: %w", shutdownCtx.Err())
	}
}
