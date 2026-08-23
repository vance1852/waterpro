package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func InsertRemediationPlan(ctx context.Context, db DBTX, plan domain.RemediationPlan) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO remediation_plans(
			id, organization_id, incident_id, title, objective, budget_cents,
			status, approved_by, version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		plan.ID, plan.OrganizationID, plan.IncidentID, plan.Title, plan.Objective,
		plan.BudgetCents, string(plan.Status), nullString(plan.ApprovedBy),
		plan.Version, formatTime(plan.CreatedAt), formatTime(plan.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert remediation plan: %w", err)
	}
	return nil
}

func scanRemediationPlan(scanner interface{ Scan(...any) error }) (domain.RemediationPlan, error) {
	var plan domain.RemediationPlan
	var status, created, updated string
	var approvedBy sql.NullString
	err := scanner.Scan(
		&plan.ID, &plan.OrganizationID, &plan.IncidentID, &plan.Title,
		&plan.Objective, &plan.BudgetCents, &status, &approvedBy,
		&plan.Version, &created, &updated,
	)
	if err != nil {
		return domain.RemediationPlan{}, err
	}
	plan.Status = domain.RemediationStatus(status)
	if approvedBy.Valid {
		plan.ApprovedBy = approvedBy.String
	}
	var parseErr error
	if plan.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.RemediationPlan{}, parseErr
	}
	if plan.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.RemediationPlan{}, parseErr
	}
	return plan, nil
}

const selectRemediationPlan = `
	id, organization_id, incident_id, title, objective, budget_cents,
	status, approved_by, version, created_at, updated_at`

func (s *Store) RemediationPlan(ctx context.Context, db DBTX, organizationID, planID string) (domain.RemediationPlan, error) {
	plan, err := scanRemediationPlan(db.QueryRowContext(ctx, "SELECT "+selectRemediationPlan+" FROM remediation_plans WHERE organization_id = ? AND id = ?", organizationID, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RemediationPlan{}, &domain.NotFoundError{Resource: "remediation plan", ID: planID}
	}
	if err != nil {
		return domain.RemediationPlan{}, fmt.Errorf("select remediation plan: %w", err)
	}
	return plan, nil
}

func (s *Store) TransitionRemediationPlan(ctx context.Context, tx *sql.Tx, plan domain.RemediationPlan, to domain.RemediationStatus, approver string, now time.Time) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE remediation_plans
		SET status = ?, approved_by = COALESCE(?, approved_by),
		    version = version + 1, updated_at = ?
		WHERE organization_id = ? AND id = ?`,
		string(to), nullString(approver), formatTime(now), plan.OrganizationID,
		plan.ID)
	if err != nil {
		return fmt.Errorf("transition remediation plan: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read remediation plan transition count: %w", err)
	}
	if changed != 1 {
		return &domain.ConflictError{Resource: "remediation plan", Key: plan.ID, Cause: domain.ErrConflict}
	}
	return nil
}

func InsertRemediationAction(ctx context.Context, db DBTX, action domain.RemediationAction) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO remediation_actions(
			id, plan_id, organization_id, idempotency_key, description,
			status, attempt_count, last_error, version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		action.ID, action.PlanID, action.OrganizationID, action.IdempotencyKey,
		action.Description, string(action.Status), action.AttemptCount, action.LastError,
		action.Version, formatTime(action.CreatedAt), formatTime(action.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert remediation action: %w", err)
	}
	return nil
}

func (s *Store) CompleteRemediationAction(ctx context.Context, tx *sql.Tx, organizationID, actionID string, expectedVersion int64, success bool, cause string, now time.Time) error {
	status := domain.ActionSucceeded
	if !success {
		status = domain.ActionFailed
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE remediation_actions
		SET status = ?, attempt_count = attempt_count + 1, last_error = ?,
		    version = version + 1, updated_at = ?
		WHERE organization_id = ? AND id = ? AND version = ?
		  AND status IN ('pending','in_progress','failed')`,
		string(status), cause, formatTime(now), organizationID, actionID, expectedVersion)
	if err != nil {
		return fmt.Errorf("complete remediation action: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read remediation action update count: %w", err)
	}
	if changed != 1 {
		return &domain.ConflictError{Resource: "remediation action", Key: actionID, Cause: domain.ErrConflict}
	}
	return nil
}

func (s *Store) CountActionsByStatus(ctx context.Context, db DBTX, organizationID, planID string) (map[domain.ActionStatus]int, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM remediation_actions
		WHERE organization_id = ? AND plan_id = ?
		GROUP BY status`, organizationID, planID)
	if err != nil {
		return nil, fmt.Errorf("count remediation actions by status: %w", err)
	}
	defer rows.Close()
	counts := make(map[domain.ActionStatus]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan remediation action count: %w", err)
		}
		counts[domain.ActionStatus(status)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate remediation action counts: %w", err)
	}
	return counts, nil
}
