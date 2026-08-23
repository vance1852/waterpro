package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func InsertPermit(ctx context.Context, db DBTX, permit domain.Permit) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO permits(
			id, organization_id, source_id, holder_name, reference,
			valid_from, valid_until, daily_volume_limit_liters,
			status, version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		permit.ID, permit.OrganizationID, permit.SourceID, permit.HolderName, permit.Reference,
		formatTime(permit.ValidFrom), formatTime(permit.ValidUntil), permit.DailyVolumeLimitLiters,
		string(permit.Status), permit.Version, formatTime(permit.CreatedAt), formatTime(permit.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert permit: %w", err)
	}
	return nil
}

func scanPermit(scanner interface{ Scan(...any) error }) (domain.Permit, error) {
	var permit domain.Permit
	var validFrom, validUntil, status, created, updated string
	err := scanner.Scan(
		&permit.ID, &permit.OrganizationID, &permit.SourceID, &permit.HolderName,
		&permit.Reference, &validFrom, &validUntil, &permit.DailyVolumeLimitLiters,
		&status, &permit.Version, &created, &updated,
	)
	if err != nil {
		return domain.Permit{}, err
	}
	permit.Status = domain.PermitStatus(status)
	var parseErr error
	if permit.ValidFrom, parseErr = parseTime(validFrom); parseErr != nil {
		return domain.Permit{}, parseErr
	}
	if permit.ValidUntil, parseErr = parseTime(validUntil); parseErr != nil {
		return domain.Permit{}, parseErr
	}
	if permit.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.Permit{}, parseErr
	}
	if permit.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.Permit{}, parseErr
	}
	return permit, nil
}

const selectPermit = `
	id, organization_id, source_id, holder_name, reference,
	valid_from, valid_until, daily_volume_limit_liters,
	status, version, created_at, updated_at`

func (s *Store) Permit(ctx context.Context, db DBTX, organizationID, permitID string) (domain.Permit, error) {
	permit, err := scanPermit(db.QueryRowContext(ctx, "SELECT "+selectPermit+" FROM permits WHERE organization_id = ? AND id = ?", organizationID, permitID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Permit{}, &domain.NotFoundError{Resource: "permit", ID: permitID}
	}
	if err != nil {
		return domain.Permit{}, fmt.Errorf("select permit: %w", err)
	}
	return permit, nil
}

func (s *Store) TransitionPermit(ctx context.Context, tx *sql.Tx, permit domain.Permit, to domain.PermitStatus, now time.Time) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE permits
		SET status = ?, version = version + 1, updated_at = ?
		WHERE organization_id = ? AND id = ? AND status = ? AND version = ?`,
		string(to), formatTime(now), permit.OrganizationID, permit.ID, string(permit.Status), permit.Version)
	if err != nil {
		return fmt.Errorf("transition permit: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read permit transition count: %w", err)
	}
	if changed != 1 {
		return &domain.ConflictError{Resource: "permit", Key: permit.ID, Cause: domain.ErrConflict}
	}
	return nil
}

func (s *Store) DailyDischargeVolume(ctx context.Context, db DBTX, permitID string, dayStart, dayEnd time.Time) (int64, error) {
	var total int64
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(volume_liters), 0)
		FROM discharge_events
		WHERE permit_id = ? AND occurred_at >= ? AND occurred_at < ?`,
		permitID, formatTime(dayStart), formatTime(dayEnd)).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("sum daily discharge volume: %w", err)
	}
	return total, nil
}

func InsertDischargeEvent(ctx context.Context, db DBTX, event domain.DischargeEvent) (bool, error) {
	result, err := db.ExecContext(ctx, `
		INSERT INTO discharge_events(
			id, organization_id, permit_id, idempotency_key,
			volume_liters, occurred_at, reported_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(organization_id, permit_id, idempotency_key) DO NOTHING`,
		event.ID, event.OrganizationID, event.PermitID, event.IdempotencyKey,
		event.VolumeLiters, formatTime(event.OccurredAt), event.ReportedBy, formatTime(event.CreatedAt),
	)
	if err != nil {
		return false, fmt.Errorf("insert discharge event: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read discharge insert count: %w", err)
	}
	return changed == 1, nil
}

func (s *Store) ExistingDischargeByKey(ctx context.Context, db DBTX, organizationID, permitID, key string) (domain.DischargeEvent, error) {
	var event domain.DischargeEvent
	var occurred, created string
	err := db.QueryRowContext(ctx, `
		SELECT id, organization_id, permit_id, idempotency_key,
		       volume_liters, occurred_at, reported_by, created_at
		FROM discharge_events
		WHERE organization_id = ? AND permit_id = ? AND idempotency_key = ?`,
		organizationID, permitID, key).Scan(
		&event.ID, &event.OrganizationID, &event.PermitID, &event.IdempotencyKey,
		&event.VolumeLiters, &occurred, &event.ReportedBy, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DischargeEvent{}, &domain.NotFoundError{Resource: "discharge event", ID: key}
	}
	if err != nil {
		return domain.DischargeEvent{}, fmt.Errorf("select discharge by idempotency key: %w", err)
	}
	if event.OccurredAt, err = parseTime(occurred); err != nil {
		return domain.DischargeEvent{}, err
	}
	if event.CreatedAt, err = parseTime(created); err != nil {
		return domain.DischargeEvent{}, err
	}
	return event, nil
}
