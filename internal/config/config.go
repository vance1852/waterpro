package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr           string
	DatabasePath       string
	SessionTTL         time.Duration
	WorkerPollInterval time.Duration
	WorkerLease        time.Duration
	ShutdownTimeout    time.Duration
	LogLevel           slog.Level
	WorkerConcurrency  int
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:          env("WATERPRO_HTTP_ADDR", ":8080"),
		DatabasePath:      env("WATERPRO_DATABASE_PATH", "waterpro.db"),
		ShutdownTimeout:   10 * time.Second,
		WorkerConcurrency: 2,
	}
	var err error
	if cfg.SessionTTL, err = duration("WATERPRO_SESSION_TTL", 12*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.WorkerPollInterval, err = duration("WATERPRO_WORKER_POLL_INTERVAL", 2*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.WorkerLease, err = duration("WATERPRO_WORKER_LEASE", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = duration("WATERPRO_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if cfg.WorkerConcurrency, err = integer("WATERPRO_WORKER_CONCURRENCY", cfg.WorkerConcurrency); err != nil {
		return Config{}, err
	}
	switch strings.ToLower(env("WATERPRO_LOG_LEVEL", "info")) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("WATERPRO_LOG_LEVEL: unsupported value")
	}
	if strings.TrimSpace(cfg.HTTPAddr) == "" {
		return Config{}, fmt.Errorf("WATERPRO_HTTP_ADDR: cannot be empty")
	}
	if strings.TrimSpace(cfg.DatabasePath) == "" {
		return Config{}, fmt.Errorf("WATERPRO_DATABASE_PATH: cannot be empty")
	}
	if cfg.WorkerConcurrency < 1 || cfg.WorkerConcurrency > 32 {
		return Config{}, fmt.Errorf("WATERPRO_WORKER_CONCURRENCY: must be between 1 and 32")
	}
	if cfg.WorkerLease <= cfg.WorkerPollInterval {
		return Config{}, fmt.Errorf("WATERPRO_WORKER_LEASE: must exceed poll interval")
	}
	return cfg, nil
}

func env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func duration(name string, fallback time.Duration) (time.Duration, error) {
	value := env(name, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s: must be positive", name)
	}
	return parsed, nil
}

func integer(name string, fallback int) (int, error) {
	value := env(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}
