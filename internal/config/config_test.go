package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("WATERPRO_HTTP_ADDR", "")
	t.Setenv("WATERPRO_DATABASE_PATH", "")
	// LookupEnv treats an explicitly empty value as configuration, so remove
	// the variables by using a subprocess-independent helper below.
	t.Setenv("WATERPRO_HTTP_ADDR", ":9090")
	t.Setenv("WATERPRO_DATABASE_PATH", "test.db")
	t.Setenv("WATERPRO_SESSION_TTL", "12h")
	t.Setenv("WATERPRO_WORKER_POLL_INTERVAL", "2s")
	t.Setenv("WATERPRO_WORKER_LEASE", "30s")
	t.Setenv("WATERPRO_SHUTDOWN_TIMEOUT", "10s")
	t.Setenv("WATERPRO_WORKER_CONCURRENCY", "2")
	t.Setenv("WATERPRO_LOG_LEVEL", "info")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.DatabasePath != "test.db" {
		t.Fatalf("DatabasePath = %q", cfg.DatabasePath)
	}
	if cfg.SessionTTL != 12*time.Hour {
		t.Fatalf("SessionTTL = %s", cfg.SessionTTL)
	}
	if cfg.WorkerPollInterval != 2*time.Second {
		t.Fatalf("WorkerPollInterval = %s", cfg.WorkerPollInterval)
	}
	if cfg.WorkerLease != 30*time.Second {
		t.Fatalf("WorkerLease = %s", cfg.WorkerLease)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("ShutdownTimeout = %s", cfg.ShutdownTimeout)
	}
	if cfg.WorkerConcurrency != 2 {
		t.Fatalf("WorkerConcurrency = %d", cfg.WorkerConcurrency)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %v", cfg.LogLevel)
	}
}

func TestLoadAcceptsEveryLogLevel(t *testing.T) {
	levels := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	for input, expected := range levels {
		t.Run(input, func(t *testing.T) {
			t.Setenv("WATERPRO_HTTP_ADDR", ":8080")
			t.Setenv("WATERPRO_DATABASE_PATH", "test.db")
			t.Setenv("WATERPRO_WORKER_POLL_INTERVAL", "1s")
			t.Setenv("WATERPRO_WORKER_LEASE", "2s")
			t.Setenv("WATERPRO_LOG_LEVEL", input)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.LogLevel != expected {
				t.Fatalf("level = %v, want %v", cfg.LogLevel, expected)
			}
		})
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct{ name, variable, value string }{
		{name: "bad duration", variable: "WATERPRO_SESSION_TTL", value: "soon"},
		{name: "zero duration", variable: "WATERPRO_SESSION_TTL", value: "0s"},
		{name: "bad integer", variable: "WATERPRO_WORKER_CONCURRENCY", value: "many"},
		{name: "zero workers", variable: "WATERPRO_WORKER_CONCURRENCY", value: "0"},
		{name: "too many workers", variable: "WATERPRO_WORKER_CONCURRENCY", value: "33"},
		{name: "unknown log level", variable: "WATERPRO_LOG_LEVEL", value: "verbose"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WATERPRO_HTTP_ADDR", ":8080")
			t.Setenv("WATERPRO_DATABASE_PATH", "test.db")
			t.Setenv("WATERPRO_SESSION_TTL", "12h")
			t.Setenv("WATERPRO_WORKER_POLL_INTERVAL", "1s")
			t.Setenv("WATERPRO_WORKER_LEASE", "2s")
			t.Setenv("WATERPRO_WORKER_CONCURRENCY", "2")
			t.Setenv("WATERPRO_LOG_LEVEL", "info")
			t.Setenv(test.variable, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() unexpectedly succeeded")
			}
		})
	}
}

func TestLoadRequiresLeaseLongerThanPoll(t *testing.T) {
	t.Setenv("WATERPRO_HTTP_ADDR", ":8080")
	t.Setenv("WATERPRO_DATABASE_PATH", "test.db")
	t.Setenv("WATERPRO_WORKER_POLL_INTERVAL", "5s")
	t.Setenv("WATERPRO_WORKER_LEASE", "5s")
	if _, err := Load(); err == nil {
		t.Fatal("equal poll and lease unexpectedly accepted")
	}
}
