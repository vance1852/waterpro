package source

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

type RegisterSourceCommand struct {
	Name      string            `json:"name"`
	Kind      domain.SourceKind `json:"kind"`
	Timezone  string            `json:"timezone"`
	RequestID string            `json:"-"`
}

type RegisterZoneCommand struct {
	SourceID         string                     `json:"source_id"`
	Name             string                     `json:"name"`
	Level            domain.ProtectionZoneLevel `json:"level"`
	AreaSquareMeters int64                      `json:"area_square_meters"`
	RequestID        string                     `json:"-"`
}

type RegisterStationCommand struct {
	SourceID  string  `json:"source_id"`
	ZoneID    string  `json:"zone_id"`
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	RequestID string  `json:"-"`
}

func NewService(store *repository.Store) *Service {
	return &Service{store: store, clock: time.Now}
}

func (s *Service) RegisterWaterSource(ctx context.Context, actor domain.Actor, command RegisterSourceCommand) (domain.WaterSource, error) {
	if !actor.CanSupervise() {
		return domain.WaterSource{}, domain.ErrForbidden
	}
	now := s.clock().UTC()
	source := domain.WaterSource{
		ID:             uuid.NewString(),
		OrganizationID: actor.OrganizationID,
		Name:           strings.TrimSpace(command.Name),
		Kind:           command.Kind,
		Timezone:       strings.TrimSpace(command.Timezone),
		Active:         true,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := source.Validate(); err != nil {
		return domain.WaterSource{}, err
	}
	if strings.TrimSpace(command.RequestID) == "" {
		return domain.WaterSource{}, domain.NewValidationError("register water source", domain.FieldViolation{Field: "request_id", Rule: "is required"})
	}
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := repository.InsertWaterSource(ctx, tx, source); err != nil {
			return err
		}
		metadata, _ := json.Marshal(map[string]any{"kind": source.Kind, "timezone": source.Timezone})
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID:             uuid.NewString(),
			OrganizationID: actor.OrganizationID,
			ActorUserID:    actor.UserID,
			RequestID:      command.RequestID,
			Action:         "water_source.register",
			ObjectType:     "water_source",
			ObjectID:       source.ID,
			Outcome:        "success",
			Metadata:       string(metadata),
			OccurredAt:     now,
		})
	})
	if err != nil {
		return domain.WaterSource{}, fmt.Errorf("register water source: %w", err)
	}
	return source, nil
}

func (s *Service) RegisterZone(ctx context.Context, actor domain.Actor, command RegisterZoneCommand) (domain.ProtectionZone, error) {
	if !actor.CanSupervise() {
		return domain.ProtectionZone{}, domain.ErrForbidden
	}
	now := s.clock().UTC()
	zone := domain.ProtectionZone{
		ID:               uuid.NewString(),
		SourceID:         command.SourceID,
		OrganizationID:   actor.OrganizationID,
		Name:             strings.TrimSpace(command.Name),
		Level:            command.Level,
		AreaSquareMeters: command.AreaSquareMeters,
		Active:           true,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := zone.Validate(); err != nil {
		return domain.ProtectionZone{}, err
	}
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		source, err := s.store.WaterSource(ctx, tx, actor.OrganizationID, command.SourceID)
		if err != nil {
			return err
		}
		if !source.Active {
			return &domain.ConflictError{Resource: "water source", Key: source.ID, Cause: errors.New("inactive sources cannot receive zones")}
		}
		if err := repository.InsertProtectionZone(ctx, tx, zone); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "protection_zone.register", ObjectType: "protection_zone",
			ObjectID: zone.ID, Outcome: "success", Metadata: fmt.Sprintf(`{"source_id":%q,"level":%q}`, zone.SourceID, zone.Level), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.ProtectionZone{}, fmt.Errorf("register protection zone: %w", err)
	}
	return zone, nil
}

func (s *Service) RegisterStation(ctx context.Context, actor domain.Actor, command RegisterStationCommand) (domain.MonitoringStation, error) {
	if !actor.CanSupervise() {
		return domain.MonitoringStation{}, domain.ErrForbidden
	}
	now := s.clock().UTC()
	station := domain.MonitoringStation{
		ID: uuid.NewString(), SourceID: command.SourceID, ZoneID: command.ZoneID,
		OrganizationID: actor.OrganizationID, Code: strings.ToUpper(strings.TrimSpace(command.Code)),
		Name: strings.TrimSpace(command.Name), Latitude: command.Latitude, Longitude: command.Longitude,
		Active: true, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := station.Validate(); err != nil {
		return domain.MonitoringStation{}, err
	}
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		source, err := s.store.WaterSource(ctx, tx, actor.OrganizationID, station.SourceID)
		if err != nil {
			return err
		}
		zone, err := s.store.ProtectionZone(ctx, tx, actor.OrganizationID, station.ZoneID)
		if err != nil {
			return err
		}
		if zone.SourceID != source.ID {
			return &domain.ConflictError{Resource: "protection zone", Key: zone.ID, Cause: errors.New("zone belongs to a different water source")}
		}
		if !source.Active || !zone.Active {
			return &domain.ConflictError{Resource: "monitoring station", Key: station.Code, Cause: errors.New("source and zone must be active")}
		}
		if err := repository.InsertMonitoringStation(ctx, tx, station); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "monitoring_station.register", ObjectType: "monitoring_station",
			ObjectID: station.ID, Outcome: "success", Metadata: fmt.Sprintf(`{"source_id":%q,"zone_id":%q}`, station.SourceID, station.ZoneID), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.MonitoringStation{}, fmt.Errorf("register monitoring station: %w", err)
	}
	return station, nil
}

func (s *Service) ListSources(ctx context.Context, actor domain.Actor, activeOnly bool, page domain.PageRequest) (domain.Page[domain.WaterSource], error) {
	if err := actor.Validate(); err != nil {
		return domain.Page[domain.WaterSource]{}, err
	}
	result, err := s.store.ListWaterSources(ctx, actor.OrganizationID, activeOnly, page)
	if err != nil {
		return domain.Page[domain.WaterSource]{}, fmt.Errorf("list water sources: %w", err)
	}
	return result, nil
}

func (s *Service) ListStations(ctx context.Context, actor domain.Actor, sourceID string) ([]domain.MonitoringStation, error) {
	if _, err := s.store.WaterSource(ctx, s.store.DB(), actor.OrganizationID, sourceID); err != nil {
		return nil, err
	}
	stations, err := s.store.ListStations(ctx, actor.OrganizationID, sourceID, true)
	if err != nil {
		return nil, fmt.Errorf("list monitoring stations: %w", err)
	}
	return stations, nil
}
