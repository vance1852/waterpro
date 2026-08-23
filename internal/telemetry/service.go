package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vance1852/waterpro/internal/audit"
	"github.com/vance1852/waterpro/internal/domain"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
)

type Service struct {
	store *repository.Store
	clock func() time.Time
}

type IngestCommand struct {
	StationID  string    `json:"station_id"`
	ExternalID string    `json:"external_id"`
	Parameter  string    `json:"parameter"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
	Threshold  float64   `json:"threshold"`
	ObservedAt time.Time `json:"observed_at"`
	RequestID  string    `json:"-"`
}

func NewService(store *repository.Store) *Service { return &Service{store: store, clock: time.Now} }

func (s *Service) Ingest(ctx context.Context, actor domain.Actor, command IngestCommand) (domain.TelemetryReading, bool, error) {
	now := s.clock().UTC()
	reading := domain.TelemetryReading{
		ID: uuid.NewString(), OrganizationID: actor.OrganizationID, StationID: command.StationID,
		ExternalID: strings.TrimSpace(command.ExternalID), Parameter: strings.TrimSpace(command.Parameter),
		Value: command.Value, Unit: strings.TrimSpace(command.Unit), Threshold: command.Threshold,
		ObservedAt: command.ObservedAt.UTC(), ReceivedAt: now,
	}
	if err := reading.Validate(now); err != nil {
		return domain.TelemetryReading{}, false, err
	}
	created := false
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		station, err := s.store.MonitoringStation(ctx, tx, actor.OrganizationID, command.StationID)
		if err != nil {
			return err
		}
		if !station.Active {
			return &domain.ConflictError{Resource: "monitoring station", Key: station.ID, Cause: fmt.Errorf("station is inactive")}
		}
		created, err = repository.InsertTelemetryReading(ctx, tx, reading)
		if err != nil {
			return err
		}
		if !created {
			return nil
		}
		if reading.ExceedsThreshold() {
			job := domain.AlertJob{
				ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ReadingID: reading.ID,
				Status: domain.JobPending, MaxAttempts: 5, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := repository.InsertAlertJob(ctx, tx, job); err != nil {
				return err
			}
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "telemetry.ingest", ObjectType: "telemetry_reading", ObjectID: reading.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"station_id":%q,"parameter":%q,"exceeds":%t}`, reading.StationID, reading.Parameter, reading.ExceedsThreshold()), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.TelemetryReading{}, false, fmt.Errorf("ingest telemetry reading: %w", err)
	}
	return reading, created, nil
}
