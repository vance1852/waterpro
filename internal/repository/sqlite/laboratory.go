package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func InsertLabResult(ctx context.Context, db DBTX, result domain.LabResult) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO lab_results(
			id, organization_id, sample_id, parameter, value, unit, method_code,
			detection_limit, regulatory_limit, status, analyst_user_id, reviewer_user_id,
			version, measured_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		result.ID, result.OrganizationID, result.SampleID, result.Parameter, result.Value,
		result.Unit, result.MethodCode, result.DetectionLimit, result.RegulatoryLimit,
		string(result.Status), result.AnalystUserID, nullString(result.ReviewerUserID),
		result.Version, formatTime(result.MeasuredAt), formatTime(result.CreatedAt), formatTime(result.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert laboratory result: %w", err)
	}
	return nil
}

func scanLabResult(scanner interface{ Scan(...any) error }) (domain.LabResult, error) {
	var result domain.LabResult
	var status, measured, created, updated string
	var reviewer sql.NullString
	err := scanner.Scan(
		&result.ID, &result.OrganizationID, &result.SampleID, &result.Parameter,
		&result.Value, &result.Unit, &result.MethodCode, &result.DetectionLimit,
		&result.RegulatoryLimit, &status, &result.AnalystUserID, &reviewer,
		&result.Version, &measured, &created, &updated,
	)
	if err != nil {
		return domain.LabResult{}, err
	}
	result.Status = domain.LabResultStatus(status)
	if reviewer.Valid {
		result.ReviewerUserID = reviewer.String
	}
	var parseErr error
	if result.MeasuredAt, parseErr = parseTime(measured); parseErr != nil {
		return domain.LabResult{}, parseErr
	}
	if result.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.LabResult{}, parseErr
	}
	if result.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.LabResult{}, parseErr
	}
	return result, nil
}

const selectLabResult = `
	id, organization_id, sample_id, parameter, value, unit, method_code,
	detection_limit, regulatory_limit, status, analyst_user_id, reviewer_user_id,
	version, measured_at, created_at, updated_at`

func (s *Store) LabResult(ctx context.Context, db DBTX, organizationID, resultID string) (domain.LabResult, error) {
	result, err := scanLabResult(db.QueryRowContext(ctx, "SELECT "+selectLabResult+" FROM lab_results WHERE organization_id = ? AND id = ?", organizationID, resultID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LabResult{}, &domain.NotFoundError{Resource: "laboratory result", ID: resultID}
	}
	if err != nil {
		return domain.LabResult{}, fmt.Errorf("select laboratory result: %w", err)
	}
	return result, nil
}

func (s *Store) TransitionLabResult(ctx context.Context, tx *sql.Tx, result domain.LabResult, to domain.LabResultStatus, reviewer string, now time.Time) error {
	update, err := tx.ExecContext(ctx, `
		UPDATE lab_results
		SET status = ?, reviewer_user_id = ?, version = version + 1, updated_at = ?
		WHERE organization_id = ? AND id = ?`,
		string(to), nullString(reviewer), formatTime(now), result.OrganizationID,
		result.ID)
	if err != nil {
		return fmt.Errorf("transition laboratory result: %w", err)
	}
	changed, err := update.RowsAffected()
	if err != nil {
		return fmt.Errorf("read laboratory result transition count: %w", err)
	}
	if changed != 1 {
		return &domain.ConflictError{Resource: "laboratory result", Key: result.ID, Cause: domain.ErrConflict}
	}
	return nil
}

func (s *Store) CountOpenExceedances(ctx context.Context, db DBTX, organizationID, sourceID string) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM incidents
		WHERE organization_id = ? AND source_id = ?
		  AND originating_result_id IS NOT NULL
		  AND status <> 'resolved'`, organizationID, sourceID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count open exceedances: %w", err)
	}
	return count, nil
}
