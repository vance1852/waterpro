package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vance1852/waterpro/internal/auth"
	"github.com/vance1852/waterpro/internal/config"
	"github.com/vance1852/waterpro/internal/httpapi"
	"github.com/vance1852/waterpro/internal/incident"
	"github.com/vance1852/waterpro/internal/platform"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
	"github.com/vance1852/waterpro/internal/sampling"
	"github.com/vance1852/waterpro/internal/source"
	"github.com/vance1852/waterpro/internal/telemetry"
	"github.com/vance1852/waterpro/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	store, err := repository.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()
	if err := platform.Bootstrap(ctx, store, platform.BootstrapConfig{
		OrganizationID:     os.Getenv("WATERPRO_BOOTSTRAP_ORG_ID"),
		OrganizationName:   os.Getenv("WATERPRO_BOOTSTRAP_ORG_NAME"),
		SupervisorEmail:    os.Getenv("WATERPRO_BOOTSTRAP_EMAIL"),
		SupervisorPassword: os.Getenv("WATERPRO_BOOTSTRAP_PASSWORD"),
	}); err != nil {
		return err
	}
	authService := auth.NewService(store, cfg.SessionTTL)
	handler := httpapi.New(
		store,
		authService,
		source.NewService(store),
		sampling.NewService(store),
		incident.NewService(store, cfg.WorkerLease),
		telemetry.NewService(store),
		logger,
	).Handler()
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	workerRuntime := worker.New(
		store,
		telemetry.NewAlertProcessor(store),
		telemetry.NewLogNotifier(logger),
		logger,
		"waterpro",
		cfg.WorkerPollInterval,
		cfg.WorkerLease,
		cfg.WorkerConcurrency,
	)
	workerError := make(chan error, 1)
	go func() {
		workerError <- workerRuntime.Run(ctx)
	}()
	serveError := make(chan error, 1)
	go func() {
		logger.Info("waterpro server starting", "addr", cfg.HTTPAddr)
		serveError <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		err = <-serveError
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve http: %w", err)
		}
		logger.Info("waterpro server stopped")
		return nil
	case err := <-serveError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve http: %w", err)
	case err := <-workerError:
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("run background workers: %w", err)
	}
}
