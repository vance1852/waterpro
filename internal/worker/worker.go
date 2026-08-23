package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
)

type AlertProcessor interface {
	ProcessAlert(context.Context, domain.AlertJob) error
}
type Notifier interface {
	Deliver(context.Context, string, string, []byte) error
}

type Runtime struct {
	store       *repository.Store
	alerts      AlertProcessor
	notifier    Notifier
	logger      *slog.Logger
	owner       string
	poll        time.Duration
	lease       time.Duration
	concurrency int
}

func New(store *repository.Store, alerts AlertProcessor, notifier Notifier, logger *slog.Logger, owner string, poll, lease time.Duration, concurrency int) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	if concurrency < 1 {
		concurrency = 1
	}
	return &Runtime{store: store, alerts: alerts, notifier: notifier, logger: logger, owner: owner, poll: poll, lease: lease, concurrency: concurrency}
}

func (r *Runtime) Run(ctx context.Context) error {
	var workers sync.WaitGroup
	for index := 0; index < r.concurrency; index++ {
		workers.Add(1)
		go func(workerID int) {
			defer workers.Done()
			r.loop(ctx, fmt.Sprintf("%s-%d", r.owner, workerID))
		}(index)
	}
	<-ctx.Done()
	workers.Wait()
	return ctx.Err()
}

func (r *Runtime) loop(ctx context.Context, owner string) {
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		if err := r.processOne(ctx, owner); err != nil && !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, context.Canceled) {
			r.logger.ErrorContext(ctx, "worker iteration failed", "owner", owner, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) processOne(ctx context.Context, owner string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.alerts != nil {
		token, err := leaseToken()
		if err != nil {
			return err
		}
		job, err := r.store.ClaimAlertJob(ctx, owner, token, time.Now().UTC(), r.lease)
		if err == nil {
			processErr := r.alerts.ProcessAlert(ctx, job)
			if finishErr := r.store.FinishAlertJob(ctx, job, processErr, time.Now().UTC()); finishErr != nil {
				return errors.Join(processErr, finishErr)
			}
			return processErr
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	if r.notifier != nil {
		token, err := leaseToken()
		if err != nil {
			return err
		}
		event, err := r.store.ClaimOutboxEvent(ctx, owner, token, time.Now().UTC(), r.lease)
		if err != nil {
			return err
		}
		deliverErr := r.notifier.Deliver(ctx, event.Topic, event.IdempotencyKey, event.Payload)
		if deliverErr != nil {
			return errors.Join(deliverErr, r.store.FailOutboxEvent(ctx, event, deliverErr, time.Now().UTC()))
		}
		return r.store.CompleteOutboxEvent(ctx, event, time.Now().UTC())
	}
	return domain.ErrNotFound
}

func leaseToken() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate worker lease token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
