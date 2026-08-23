package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func InsertOutboxEvent(ctx context.Context, db DBTX, event domain.OutboxEvent) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO outbox_events(
			id, organization_id, topic, aggregate_type, aggregate_id, idempotency_key,
			payload, status, attempt_count, max_attempts, available_at,
			lease_owner, lease_token, lease_generation, lease_expires_at,
			last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.OrganizationID, event.Topic, event.AggregateType, event.AggregateID,
		event.IdempotencyKey, event.Payload, string(event.Status), event.AttemptCount,
		event.MaxAttempts, formatTime(event.AvailableAt), nullString(event.LeaseOwner),
		nullString(event.LeaseToken), event.LeaseGeneration, nullableTime(event.LeaseExpiresAt),
		event.LastError, formatTime(event.CreatedAt), formatTime(event.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func scanOutboxEvent(scanner interface{ Scan(...any) error }) (domain.OutboxEvent, error) {
	var event domain.OutboxEvent
	var status, available, created, updated string
	var leaseOwner, leaseToken, leaseExpires sql.NullString
	err := scanner.Scan(
		&event.ID, &event.OrganizationID, &event.Topic, &event.AggregateType,
		&event.AggregateID, &event.IdempotencyKey, &event.Payload, &status,
		&event.AttemptCount, &event.MaxAttempts, &available, &leaseOwner,
		&leaseToken, &event.LeaseGeneration, &leaseExpires, &event.LastError,
		&created, &updated,
	)
	if err != nil {
		return domain.OutboxEvent{}, err
	}
	event.Status = domain.OutboxStatus(status)
	if leaseOwner.Valid {
		event.LeaseOwner = leaseOwner.String
	}
	if leaseToken.Valid {
		event.LeaseToken = leaseToken.String
	}
	var parseErr error
	if event.AvailableAt, parseErr = parseTime(available); parseErr != nil {
		return domain.OutboxEvent{}, parseErr
	}
	if leaseExpires.Valid {
		value, err := parseTime(leaseExpires.String)
		if err != nil {
			return domain.OutboxEvent{}, err
		}
		event.LeaseExpiresAt = &value
	}
	if event.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.OutboxEvent{}, parseErr
	}
	if event.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.OutboxEvent{}, parseErr
	}
	return event, nil
}

const selectOutboxEvent = `
	id, organization_id, topic, aggregate_type, aggregate_id, idempotency_key,
	payload, status, attempt_count, max_attempts, available_at,
	lease_owner, lease_token, lease_generation, lease_expires_at,
	last_error, created_at, updated_at`

func (s *Store) ClaimOutboxEvent(ctx context.Context, owner, token string, now time.Time, lease time.Duration) (domain.OutboxEvent, error) {
	var claimed domain.OutboxEvent
	err := s.WithTx(ctx, nil, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT `+selectOutboxEvent+`
			FROM outbox_events
			WHERE status IN ('pending','retry')
			  AND available_at <= ?
			  AND (lease_expires_at IS NULL OR lease_expires_at <= ?)
			ORDER BY available_at ASC, created_at ASC, id ASC
			LIMIT 1`, formatTime(now), formatTime(now))
		event, err := scanOutboxEvent(row)
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.NotFoundError{Resource: "outbox event", ID: "claimable"}
		}
		if err != nil {
			return fmt.Errorf("select claimable outbox event: %w", err)
		}
		expires := now.Add(lease)
		result, err := tx.ExecContext(ctx, `
			UPDATE outbox_events
			SET status = 'sending', lease_owner = ?, lease_token = ?,
			    lease_generation = lease_generation + 1, lease_expires_at = ?, updated_at = ?
			WHERE id = ?
			  AND status IN ('pending','retry')
			  AND (lease_expires_at IS NULL OR lease_expires_at <= ?)`,
			owner, token, formatTime(expires), formatTime(now), event.ID, formatTime(now))
		if err != nil {
			return fmt.Errorf("claim outbox event: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read outbox claim count: %w", err)
		}
		if changed != 1 {
			return &domain.ConflictError{Resource: "outbox event", Key: event.ID, Cause: domain.ErrConflict}
		}
		event.Status = domain.OutboxSending
		event.LeaseOwner = owner
		event.LeaseToken = token
		event.LeaseGeneration++
		event.LeaseExpiresAt = &expires
		event.UpdatedAt = now
		claimed = event
		return nil
	})
	if err != nil {
		return domain.OutboxEvent{}, err
	}
	return claimed, nil
}

func (s *Store) CompleteOutboxEvent(ctx context.Context, event domain.OutboxEvent, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'delivered', lease_owner = NULL, lease_token = NULL,
		    lease_expires_at = NULL, last_error = '', updated_at = ?
		WHERE id = ? AND status = 'sending' AND lease_owner = ?
		  AND lease_token = ? AND lease_generation = ? AND lease_expires_at > ?`,
		formatTime(now), event.ID, event.LeaseOwner, event.LeaseToken, event.LeaseGeneration, formatTime(now))
	if err != nil {
		return fmt.Errorf("complete outbox event: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read outbox completion count: %w", err)
	}
	if changed != 1 {
		return &domain.LeaseError{Resource: "outbox event " + event.ID, Owner: event.LeaseOwner, Generation: event.LeaseGeneration}
	}
	return nil
}

func (s *Store) FailOutboxEvent(ctx context.Context, event domain.OutboxEvent, cause error, now time.Time) error {
	attempt := event.AttemptCount + 1
	status := domain.OutboxRetry
	available := now.Add(domain.RetryDelay(attempt))
	if attempt >= event.MaxAttempts {
		status = domain.OutboxDead
		available = now
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = ?, attempt_count = ?, available_at = ?, last_error = ?,
		    lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL, updated_at = ?
		WHERE id = ? AND status = 'sending' AND lease_owner = ?
		  AND lease_token = ? AND lease_generation = ?`,
		string(status), attempt, formatTime(available), truncateError(cause), formatTime(now),
		event.ID, event.LeaseOwner, event.LeaseToken, event.LeaseGeneration)
	if err != nil {
		return fmt.Errorf("fail outbox event: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read outbox failure count: %w", err)
	}
	if changed != 1 {
		return &domain.LeaseError{Resource: "outbox event " + event.ID, Owner: event.LeaseOwner, Generation: event.LeaseGeneration}
	}
	return nil
}

func truncateError(err error) string {
	if err == nil {
		return "unknown error"
	}
	message := err.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}
