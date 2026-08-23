package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func InsertTelemetryReading(ctx context.Context, db DBTX, reading domain.TelemetryReading) (bool, error) {
	result, err := db.ExecContext(ctx, `
		INSERT INTO telemetry_readings(
			id, organization_id, station_id, external_id, parameter,
			value, unit, threshold, observed_at, received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(organization_id, station_id, external_id) DO NOTHING`,
		reading.ID, reading.OrganizationID, reading.StationID, reading.ExternalID,
		reading.Parameter, reading.Value, reading.Unit, reading.Threshold,
		formatTime(reading.ObservedAt), formatTime(reading.ReceivedAt),
	)
	if err != nil {
		return false, fmt.Errorf("insert telemetry reading: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read telemetry insert count: %w", err)
	}
	return changed == 1, nil
}

func InsertAlertJob(ctx context.Context, db DBTX, job domain.AlertJob) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO alert_jobs(
			id, organization_id, reading_id, status, attempt_count, max_attempts,
			available_at, lease_owner, lease_token, lease_generation, lease_expires_at,
			last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.OrganizationID, job.ReadingID, string(job.Status), job.AttemptCount,
		job.MaxAttempts, formatTime(job.AvailableAt), nullString(job.LeaseOwner),
		nullString(job.LeaseToken), job.LeaseGeneration, nullableTime(job.LeaseExpiresAt),
		job.LastError, formatTime(job.CreatedAt), formatTime(job.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert alert job: %w", err)
	}
	return nil
}

func scanAlertJob(scanner interface{ Scan(...any) error }) (domain.AlertJob, error) {
	var job domain.AlertJob
	var status, available, created, updated string
	var leaseOwner, leaseToken, leaseExpires sql.NullString
	err := scanner.Scan(
		&job.ID, &job.OrganizationID, &job.ReadingID, &status, &job.AttemptCount,
		&job.MaxAttempts, &available, &leaseOwner, &leaseToken, &job.LeaseGeneration,
		&leaseExpires, &job.LastError, &created, &updated,
	)
	if err != nil {
		return domain.AlertJob{}, err
	}
	job.Status = domain.JobStatus(status)
	if leaseOwner.Valid {
		job.LeaseOwner = leaseOwner.String
	}
	if leaseToken.Valid {
		job.LeaseToken = leaseToken.String
	}
	var parseErr error
	if job.AvailableAt, parseErr = parseTime(available); parseErr != nil {
		return domain.AlertJob{}, parseErr
	}
	if leaseExpires.Valid {
		value, err := parseTime(leaseExpires.String)
		if err != nil {
			return domain.AlertJob{}, err
		}
		job.LeaseExpiresAt = &value
	}
	if job.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.AlertJob{}, parseErr
	}
	if job.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.AlertJob{}, parseErr
	}
	return job, nil
}

const selectAlertJob = `
	id, organization_id, reading_id, status, attempt_count, max_attempts,
	available_at, lease_owner, lease_token, lease_generation, lease_expires_at,
	last_error, created_at, updated_at`

func (s *Store) ClaimAlertJob(ctx context.Context, owner, token string, now time.Time, lease time.Duration) (domain.AlertJob, error) {
	var claimed domain.AlertJob
	err := s.WithTx(ctx, nil, func(tx *sql.Tx) error {
		job, err := scanAlertJob(tx.QueryRowContext(ctx, `
			SELECT `+selectAlertJob+` FROM alert_jobs
			WHERE status IN ('pending','retry') AND available_at <= ?
			  AND (lease_expires_at IS NULL OR lease_expires_at <= ?)
			ORDER BY available_at ASC, created_at ASC LIMIT 1`, formatTime(now), formatTime(now)))
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.NotFoundError{Resource: "alert job", ID: "claimable"}
		}
		if err != nil {
			return fmt.Errorf("select claimable alert job: %w", err)
		}
		expires := now.Add(lease)
		result, err := tx.ExecContext(ctx, `
			UPDATE alert_jobs
			SET status = 'running', lease_owner = ?, lease_token = ?,
			    lease_generation = lease_generation + 1, lease_expires_at = ?, updated_at = ?
			WHERE id = ? AND status IN ('pending','retry')
			  AND (lease_expires_at IS NULL OR lease_expires_at <= ?)`,
			owner, token, formatTime(expires), formatTime(now), job.ID, formatTime(now))
		if err != nil {
			return fmt.Errorf("claim alert job: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read alert claim count: %w", err)
		}
		if changed != 1 {
			return &domain.ConflictError{Resource: "alert job", Key: job.ID, Cause: domain.ErrConflict}
		}
		job.Status = domain.JobRunning
		job.LeaseOwner, job.LeaseToken = owner, token
		job.LeaseGeneration++
		job.LeaseExpiresAt = &expires
		claimed = job
		return nil
	})
	if err != nil {
		return domain.AlertJob{}, err
	}
	return claimed, nil
}

func (s *Store) FinishAlertJob(ctx context.Context, job domain.AlertJob, processingErr error, now time.Time) error {
	status := domain.JobSucceeded
	available := now
	attempt := job.AttemptCount
	lastError := ""
	if processingErr != nil {
		attempt++
		lastError = truncateError(processingErr)
		if attempt >= job.MaxAttempts {
			status = domain.JobDead
		} else {
			status = domain.JobRetry
			available = now.Add(domain.RetryDelay(attempt))
		}
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE alert_jobs
		SET status = ?, attempt_count = ?, available_at = ?, last_error = ?,
		    lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL, updated_at = ?
		WHERE id = ? AND status = 'running' AND lease_owner = ?
		  AND lease_token = ? AND lease_generation = ?`,
		string(status), attempt, formatTime(available), lastError, formatTime(now),
		job.ID, job.LeaseOwner, job.LeaseToken, job.LeaseGeneration)
	if err != nil {
		return fmt.Errorf("finish alert job: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read alert completion count: %w", err)
	}
	if changed != 1 {
		return &domain.LeaseError{Resource: "alert job " + job.ID, Owner: job.LeaseOwner, Generation: job.LeaseGeneration}
	}
	return nil
}
