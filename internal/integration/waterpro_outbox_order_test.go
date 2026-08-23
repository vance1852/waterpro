package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
	"github.com/vance1852/waterpro/internal/worker"
)

type wpFailingNotifier struct{}

func (wpFailingNotifier) Deliver(context.Context, string, string, []byte) error {
	return errors.New("gateway unavailable")
}

type wpNoopAlertProcessor struct{}

func (wpNoopAlertProcessor) ProcessAlert(context.Context, domain.AlertJob) error { return nil }

func TestWPOutboxDeliveryFailurePreservesRetryableEvent(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	if err := f.store.WithTx(context.Background(), nil, func(tx *sql.Tx) error {
		return repository.InsertOutboxEvent(context.Background(), tx, domain.OutboxEvent{
			ID: "wp-outbox-event", OrganizationID: "org-1", Topic: "water.alert", AggregateType: "incident", AggregateID: "incident-1", IdempotencyKey: "wp-outbox-key", Payload: []byte(`{"severity":"critical"}`), Status: domain.OutboxPending, MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatalf("seed outbox event: %v", err)
	}
	runtime := worker.New(f.store, nil, wpFailingNotifier{}, slog.Default(), "wp-test", time.Millisecond, time.Minute, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = runtime.Run(ctx)
	var status string
	var attempts int
	if err := f.store.DB().QueryRowContext(context.Background(), `SELECT status, attempt_count FROM outbox_events WHERE id = 'wp-outbox-event'`).Scan(&status, &attempts); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if status != string(domain.OutboxRetry) || attempts != 1 {
		t.Fatalf("event status=%q attempts=%d, want retry/1", status, attempts)
	}
}
