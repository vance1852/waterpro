package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vance1852/waterpro/internal/audit"
	"github.com/vance1852/waterpro/internal/domain"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
)

type AlertProcessor struct {
	store *repository.Store
	clock func() time.Time
}

func NewAlertProcessor(store *repository.Store) *AlertProcessor {
	return &AlertProcessor{store: store, clock: time.Now}
}

func (p *AlertProcessor) ProcessAlert(ctx context.Context, job domain.AlertJob) error {
	now := p.clock().UTC()
	return p.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var reading domain.TelemetryReading
		var observed, received string
		err := tx.QueryRowContext(ctx, `
			SELECT id, organization_id, station_id, external_id, parameter,
			       value, unit, threshold, observed_at, received_at
			FROM telemetry_readings
			WHERE organization_id = ? AND id = ?`, job.OrganizationID, job.ReadingID).Scan(
			&reading.ID, &reading.OrganizationID, &reading.StationID, &reading.ExternalID,
			&reading.Parameter, &reading.Value, &reading.Unit, &reading.Threshold,
			&observed, &received,
		)
		if err != nil {
			return fmt.Errorf("load alert reading: %w", err)
		}
		if reading.ObservedAt, err = time.Parse(time.RFC3339Nano, observed); err != nil {
			return fmt.Errorf("parse reading observed time: %w", err)
		}
		if reading.ReceivedAt, err = time.Parse(time.RFC3339Nano, received); err != nil {
			return fmt.Errorf("parse reading received time: %w", err)
		}
		if !reading.ExceedsThreshold() {
			return nil
		}
		var sourceID string
		if err := tx.QueryRowContext(ctx, `SELECT source_id FROM monitoring_stations WHERE organization_id = ? AND id = ?`, reading.OrganizationID, reading.StationID).Scan(&sourceID); err != nil {
			return fmt.Errorf("load reading source: %w", err)
		}
		incidentID := uuid.NewString()
		ratio := reading.Value / reading.Threshold
		severity := domain.SeverityAdvisory
		if ratio >= 1.5 {
			severity = domain.SeveritySignificant
		}
		if ratio >= 2 {
			severity = domain.SeverityCritical
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO incidents(
				id, organization_id, source_id, title, description, severity, status,
				lease_generation, version, reported_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, 'reported', 0, 1, ?, ?, ?)`,
			incidentID, reading.OrganizationID, sourceID,
			"Telemetry threshold exceedance",
			fmt.Sprintf("%s reading exceeded its configured threshold", reading.Parameter),
			string(severity), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		)
		if err != nil {
			return fmt.Errorf("create telemetry incident: %w", err)
		}
		payload, err := json.Marshal(map[string]any{
			"incident_id": incidentID, "reading_id": reading.ID, "station_id": reading.StationID,
			"parameter": reading.Parameter, "value": reading.Value, "threshold": reading.Threshold, "severity": severity,
		})
		if err != nil {
			return fmt.Errorf("encode telemetry alert: %w", err)
		}
		if err := repository.InsertOutboxEvent(ctx, tx, domain.OutboxEvent{
			ID: uuid.NewString(), OrganizationID: reading.OrganizationID, Topic: "incident.reported",
			AggregateType: "incident", AggregateID: incidentID, IdempotencyKey: "telemetry:" + reading.ID,
			Payload: payload, Status: domain.OutboxPending, MaxAttempts: 5,
			AvailableAt: now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("persist alert notification: %w", err)
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: reading.OrganizationID, ActorUserID: "system:alert-worker",
			RequestID: "alert-job:" + job.ID, Action: "telemetry.alert.evaluate",
			ObjectType: "incident", ObjectID: incidentID, Outcome: "success",
			Metadata: fmt.Sprintf(`{"reading_id":%q,"job_id":%q}`, reading.ID, job.ID), OccurredAt: now,
		})
	})
}

type LogNotifier struct {
	logger interface{ Info(string, ...any) }
}

func NewLogNotifier(logger interface{ Info(string, ...any) }) *LogNotifier {
	return &LogNotifier{logger: logger}
}

func (n *LogNotifier) Deliver(ctx context.Context, topic, idempotencyKey string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.logger.Info("outbox event delivered", "topic", topic, "idempotency_key", idempotencyKey, "payload_bytes", len(payload))
	return nil
}
