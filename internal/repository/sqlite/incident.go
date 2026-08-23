package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func InsertIncident(ctx context.Context, db DBTX, incident domain.Incident) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO incidents(
			id, organization_id, source_id, title, description, severity, status,
			commander_user_id, lease_token, lease_generation, lease_expires_at,
			version, reported_at, resolved_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		incident.ID, incident.OrganizationID, incident.SourceID, incident.Title,
		incident.Description, string(incident.Severity), string(incident.Status),
		nullString(incident.CommanderUserID), nullString(incident.LeaseToken),
		incident.LeaseGeneration, nullableTime(incident.LeaseExpiresAt), incident.Version,
		formatTime(incident.ReportedAt), nullableTime(incident.ResolvedAt),
		formatTime(incident.CreatedAt), formatTime(incident.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert incident: %w", err)
	}
	return nil
}

func scanIncident(scanner interface{ Scan(...any) error }) (domain.Incident, error) {
	var incident domain.Incident
	var severity, status, reported, created, updated string
	var commander, token, leaseExpires, resolved sql.NullString
	err := scanner.Scan(
		&incident.ID, &incident.OrganizationID, &incident.SourceID, &incident.Title,
		&incident.Description, &severity, &status, &commander, &token,
		&incident.LeaseGeneration, &leaseExpires, &incident.Version, &reported,
		&resolved, &created, &updated,
	)
	if err != nil {
		return domain.Incident{}, err
	}
	incident.Severity = domain.IncidentSeverity(severity)
	incident.Status = domain.IncidentStatus(status)
	if commander.Valid {
		incident.CommanderUserID = commander.String
	}
	if token.Valid {
		incident.LeaseToken = token.String
	}
	var parseErr error
	if incident.ReportedAt, parseErr = parseTime(reported); parseErr != nil {
		return domain.Incident{}, parseErr
	}
	if leaseExpires.Valid {
		value, err := parseTime(leaseExpires.String)
		if err != nil {
			return domain.Incident{}, err
		}
		incident.LeaseExpiresAt = &value
	}
	if resolved.Valid {
		value, err := parseTime(resolved.String)
		if err != nil {
			return domain.Incident{}, err
		}
		incident.ResolvedAt = &value
	}
	if incident.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.Incident{}, parseErr
	}
	if incident.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.Incident{}, parseErr
	}
	return incident, nil
}

const selectIncident = `
	id, organization_id, source_id, title, description, severity, status,
	commander_user_id, lease_token, lease_generation, lease_expires_at,
	version, reported_at, resolved_at, created_at, updated_at`

func (s *Store) Incident(ctx context.Context, db DBTX, organizationID, incidentID string) (domain.Incident, error) {
	incident, err := scanIncident(db.QueryRowContext(ctx, "SELECT "+selectIncident+" FROM incidents WHERE organization_id = ? AND id = ?", organizationID, incidentID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Incident{}, &domain.NotFoundError{Resource: "incident", ID: incidentID}
	}
	if err != nil {
		return domain.Incident{}, fmt.Errorf("select incident: %w", err)
	}
	return incident, nil
}

func (s *Store) ClaimIncident(ctx context.Context, organizationID, incidentID, owner, token string, now time.Time, lease time.Duration) (domain.Incident, error) {
	var claimed domain.Incident
	err := s.WithTx(ctx, nil, func(tx *sql.Tx) error {
		incident, err := s.Incident(ctx, tx, organizationID, incidentID)
		if err != nil {
			return err
		}
		if incident.Status == domain.IncidentResolved {
			return &domain.ConflictError{Resource: "incident", Key: incident.ID, Cause: errors.New("resolved incident cannot be claimed")}
		}
		if incident.LeaseExpiresAt != nil && incident.LeaseExpiresAt.After(now) && incident.CommanderUserID != owner {
			return &domain.ConflictError{Resource: "incident command", Key: incident.ID, Cause: errors.New("another commander holds the active lease")}
		}
		expires := now.Add(lease)
		result, err := tx.ExecContext(ctx, `
			UPDATE incidents
			SET commander_user_id = ?, lease_token = ?, lease_generation = lease_generation + 1,
			    lease_expires_at = ?, version = version + 1, updated_at = ?
			WHERE organization_id = ? AND id = ? AND version = ?
			  AND (lease_expires_at IS NULL OR lease_expires_at <= ? OR commander_user_id = ?)`,
			owner, token, formatTime(expires), formatTime(now), organizationID,
			incident.ID, incident.Version, formatTime(now), owner)
		if err != nil {
			return fmt.Errorf("claim incident command: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read incident claim count: %w", err)
		}
		if changed != 1 {
			return &domain.ConflictError{Resource: "incident command", Key: incident.ID, Cause: domain.ErrConflict}
		}
		incident.CommanderUserID = owner
		incident.LeaseToken = token
		incident.LeaseGeneration++
		incident.LeaseExpiresAt = &expires
		incident.Version++
		incident.UpdatedAt = now
		claimed = incident
		return nil
	})
	if err != nil {
		return domain.Incident{}, err
	}
	return claimed, nil
}

func (s *Store) TransitionIncident(ctx context.Context, tx *sql.Tx, incident domain.Incident, to domain.IncidentStatus, actorID, token string, now time.Time) error {
	var resolvedAt any
	if to == domain.IncidentResolved {
		resolvedAt = formatTime(now)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE incidents
		SET status = ?, resolved_at = ?, version = version + 1, updated_at = ?
		WHERE organization_id = ? AND id = ? AND status = ? AND version = ?
		  AND commander_user_id = ? AND lease_token = ? AND lease_generation = ?
		  AND lease_expires_at > ?`,
		string(to), resolvedAt, formatTime(now), incident.OrganizationID, incident.ID,
		string(incident.Status), incident.Version, actorID, token, incident.LeaseGeneration, formatTime(now))
	if err != nil {
		return fmt.Errorf("transition incident: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read incident transition count: %w", err)
	}
	if changed != 1 {
		return &domain.LeaseError{Resource: "incident " + incident.ID, Owner: actorID, Generation: incident.LeaseGeneration}
	}
	return nil
}

func InsertContainmentAssignment(ctx context.Context, db DBTX, assignment domain.ContainmentAssignment) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO containment_assignments(
			id, incident_id, organization_id, resource_code, assignee_user_id,
			status, lease_token, lease_generation, lease_expires_at,
			version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		assignment.ID, assignment.IncidentID, assignment.OrganizationID,
		assignment.ResourceCode, assignment.AssigneeUserID, string(assignment.Status),
		nullString(assignment.LeaseToken), assignment.LeaseGeneration,
		nullableTime(assignment.LeaseExpiresAt), assignment.Version,
		formatTime(assignment.CreatedAt), formatTime(assignment.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert containment assignment: %w", err)
	}
	return nil
}

func (s *Store) CountIncompleteAssignments(ctx context.Context, db DBTX, organizationID, incidentID string) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM containment_assignments
		WHERE organization_id = ? AND incident_id = ? AND status NOT IN ('completed','cancelled')`,
		organizationID, incidentID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count incomplete containment assignments: %w", err)
	}
	return count, nil
}

func (s *Store) CountOpenRemediationActions(ctx context.Context, db DBTX, organizationID, incidentID string) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM remediation_actions ra
		JOIN remediation_plans rp ON rp.id = ra.plan_id
		WHERE rp.organization_id = ? AND rp.incident_id = ?
		  AND ra.status NOT IN ('succeeded')`, organizationID, incidentID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count open remediation actions: %w", err)
	}
	return count, nil
}
