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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/config"
	"kitchen-api/internal/kitchen/handlers"
	"kitchen-api/internal/kitchen/repository"
	"kitchen-api/internal/kitchen/service"
	"kitchen-api/internal/kitchen/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("kitchen stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return err
	}

	repo := repository.New(pool)
	establishmentService := service.NewEstablishmentService(repo)
	menuService := service.NewMenuService(repo)
	orderService := service.NewOrderService(repo)

	handler := handlers.New(
		establishmentService,
		menuService,
		orderService,
		logger,
	)

	spec, err := api.GetSwagger()
	if err != nil {
		return err
	}

	strictHandler := api.NewStrictHandlerWithOptions(
		handler,
		nil,
		handler.StrictServerOptions(),
	)

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(handler.RequestLogger)

	routes := api.HandlerFromMux(strictHandler, router)
	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           handler.OpenAPIValidator(spec)(routes),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	timeoutWorker := worker.NewConfirmationTimeout(
		orderService,
		cfg.ConfirmationTimeout,
		cfg.TimeoutScanInterval,
		logger,
	)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		timeoutWorker.Run(runCtx)
	}()

	serverError := make(chan error, 1)
	go func() {
		logger.Info("kitchen started", "address", cfg.HTTPAddress)
		serverError <- server.ListenAndServe()
	}()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serverError:
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		cfg.ShutdownTimeout,
	)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}

	<-workerDone
	return serveErr
}
