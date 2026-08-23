package audit

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vance1852/waterpro/internal/domain"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
)

type Recorder struct {
	store *repository.Store
	clock func() time.Time
}

func New(store *repository.Store) *Recorder {
	return &Recorder{store: store, clock: time.Now}
}

func NewWithClock(store *repository.Store, clock func() time.Time) *Recorder {
	if clock == nil {
		clock = time.Now
	}
	return &Recorder{store: store, clock: clock}
}

func (r *Recorder) Record(ctx context.Context, event domain.AuditEvent) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = r.clock().UTC()
	}
	if event.OrganizationID == "" || event.ActorUserID == "" || event.Action == "" || event.ObjectType == "" || event.ObjectID == "" {
		return domain.NewValidationError("record audit event", domain.FieldViolation{Field: "event", Rule: "organization, actor, action, object type and object id are required"})
	}
	if event.RequestID == "" {
		return domain.NewValidationError("record audit event", domain.FieldViolation{Field: "request_id", Rule: "is required"})
	}
	if event.Outcome == "" {
		event.Outcome = "success"
	}
	if event.Metadata == "" {
		event.Metadata = "{}"
	}
	if err := Insert(ctx, r.store.DB(), event); err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	return nil
}

func Insert(ctx context.Context, db repository.DBTX, event domain.AuditEvent) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO audit_events(
			id, organization_id, actor_user_id, request_id, action,
			object_type, object_id, outcome, metadata, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.OrganizationID,
		event.ActorUserID,
		event.RequestID,
		event.Action,
		event.ObjectType,
		event.ObjectID,
		event.Outcome,
		event.Metadata,
		event.OccurredAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func ListForObject(ctx context.Context, db repository.DBTX, organizationID, objectType, objectID string, limit int) ([]domain.AuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, organization_id, actor_user_id, request_id, action,
		       object_type, object_id, outcome, metadata, occurred_at
		FROM audit_events
		WHERE organization_id = ? AND object_type = ? AND object_id = ?
		ORDER BY occurred_at DESC, id DESC
		LIMIT ?`, organizationID, objectType, objectID, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.AuditEvent, 0)
	for rows.Next() {
		var event domain.AuditEvent
		var occurred string
		if err := rows.Scan(
			&event.ID,
			&event.OrganizationID,
			&event.ActorUserID,
			&event.RequestID,
			&event.Action,
			&event.ObjectType,
			&event.ObjectID,
			&event.Outcome,
			&event.Metadata,
			&occurred,
		); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, occurred)
		if err != nil {
			return nil, fmt.Errorf("parse audit timestamp: %w", err)
		}
		event.OccurredAt = parsed
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}

func CountByRequest(ctx context.Context, db repository.DBTX, requestID string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE request_id = ?", requestID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count audit events by request: %w", err)
	}
	return count, nil
}

func DeleteBefore(ctx context.Context, tx *sql.Tx, organizationID string, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM audit_events
		WHERE id IN (
			SELECT id FROM audit_events
			WHERE organization_id = ? AND occurred_at < ?
			ORDER BY occurred_at ASC
			LIMIT ?
		)`, organizationID, cutoff.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return 0, fmt.Errorf("delete old audit events: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read deleted audit count: %w", err)
	}
	return count, nil
}
