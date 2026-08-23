package permit

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

type CreateCommand struct {
	SourceID               string
	HolderName             string
	Reference              string
	ValidFrom              time.Time
	ValidUntil             time.Time
	DailyVolumeLimitLiters int64
	RequestID              string
}

type ReportDischargeCommand struct {
	PermitID       string
	IdempotencyKey string
	VolumeLiters   int64
	OccurredAt     time.Time
	RequestID      string
}

func NewService(store *repository.Store) *Service {
	return &Service{store: store, clock: time.Now}
}

func (s *Service) Create(ctx context.Context, actor domain.Actor, command CreateCommand) (domain.Permit, error) {
	if !actor.CanSupervise() {
		return domain.Permit{}, domain.ErrForbidden
	}
	now := s.clock().UTC()
	permit := domain.Permit{
		ID: uuid.NewString(), OrganizationID: actor.OrganizationID, SourceID: command.SourceID,
		HolderName: strings.TrimSpace(command.HolderName), Reference: strings.TrimSpace(command.Reference),
		ValidFrom: command.ValidFrom.UTC(), ValidUntil: command.ValidUntil.UTC(),
		DailyVolumeLimitLiters: command.DailyVolumeLimitLiters, Status: domain.PermitDraft,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := permit.Validate(); err != nil {
		return domain.Permit{}, err
	}
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		source, err := s.store.WaterSource(ctx, tx, actor.OrganizationID, command.SourceID)
		if err != nil {
			return err
		}
		if !source.Active {
			return &domain.ConflictError{Resource: "water source", Key: source.ID, Cause: errors.New("inactive source cannot receive a permit")}
		}
		if err := repository.InsertPermit(ctx, tx, permit); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "permit.create", ObjectType: "permit", ObjectID: permit.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"source_id":%q,"reference":%q}`, permit.SourceID, permit.Reference), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.Permit{}, fmt.Errorf("create permit: %w", err)
	}
	return permit, nil
}

func (s *Service) Activate(ctx context.Context, actor domain.Actor, permitID, requestID string) error {
	if !actor.CanSupervise() {
		return domain.ErrForbidden
	}
	now := s.clock().UTC()
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		permit, err := s.store.Permit(ctx, tx, actor.OrganizationID, permitID)
		if err != nil {
			return err
		}
		if err := permit.CanTransition(domain.PermitActive, now); err != nil {
			return err
		}
		open, err := s.store.CountOpenExceedances(ctx, tx, actor.OrganizationID, permit.SourceID)
		if err != nil {
			return err
		}
		if open > 0 {
			return &domain.ConflictError{Resource: "permit", Key: permit.ID, Cause: errors.New("unresolved water quality exceedances block activation")}
		}
		if err := s.store.TransitionPermit(ctx, tx, permit, domain.PermitActive, now); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: requestID, Action: "permit.activate", ObjectType: "permit", ObjectID: permit.ID,
			Outcome: "success", Metadata: "{}", OccurredAt: now,
		})
	})
	if err != nil {
		return fmt.Errorf("activate permit: %w", err)
	}
	return nil
}

func (s *Service) Suspend(ctx context.Context, actor domain.Actor, permitID, reason, requestID string) error {
	if !actor.CanSupervise() {
		return domain.ErrForbidden
	}
	if len(strings.TrimSpace(reason)) < 5 {
		return domain.NewValidationError("suspend permit", domain.FieldViolation{Field: "reason", Rule: "must contain at least 5 characters"})
	}
	now := s.clock().UTC()
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		permit, err := s.store.Permit(ctx, tx, actor.OrganizationID, permitID)
		if err != nil {
			return err
		}
		if err := permit.CanTransition(domain.PermitSuspended, now); err != nil {
			return err
		}
		if err := s.store.TransitionPermit(ctx, tx, permit, domain.PermitSuspended, now); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"permit_id": permit.ID, "reason": reason})
		if err := repository.InsertOutboxEvent(ctx, tx, domain.OutboxEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, Topic: "permit.suspended",
			AggregateType: "permit", AggregateID: permit.ID, IdempotencyKey: "suspend:" + permit.ID + ":" + fmt.Sprint(permit.Version),
			Payload: payload, Status: domain.OutboxPending, MaxAttempts: 5, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: requestID, Action: "permit.suspend", ObjectType: "permit", ObjectID: permit.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"reason":%q}`, reason), OccurredAt: now,
		})
	})
	if err != nil {
		return fmt.Errorf("suspend permit: %w", err)
	}
	return nil
}

func (s *Service) ReportDischarge(ctx context.Context, actor domain.Actor, command ReportDischargeCommand) (domain.DischargeEvent, bool, error) {
	when := command.OccurredAt.UTC()
	if command.OccurredAt.IsZero() {
		when = s.clock().UTC()
	}
	event := domain.DischargeEvent{
		ID: uuid.NewString(), OrganizationID: actor.OrganizationID, PermitID: command.PermitID,
		IdempotencyKey: strings.TrimSpace(command.IdempotencyKey), VolumeLiters: command.VolumeLiters,
		OccurredAt: when, ReportedBy: actor.UserID, CreatedAt: s.clock().UTC(),
	}
	if err := event.Validate(); err != nil {
		return domain.DischargeEvent{}, false, err
	}
	created := false
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		permit, err := s.store.Permit(ctx, tx, actor.OrganizationID, command.PermitID)
		if err != nil {
			return err
		}
		if permit.Status != domain.PermitActive || when.Before(permit.ValidFrom) || !when.Before(permit.ValidUntil) {
			return &domain.ConflictError{Resource: "permit", Key: permit.ID, Cause: errors.New("permit is not active at discharge time")}
		}
		dayStart := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, time.UTC)
		total, err := s.store.DailyDischargeVolume(ctx, tx, permit.ID, dayStart, dayStart.Add(24*time.Hour))
		if err != nil {
			return err
		}
		if total+event.VolumeLiters > permit.DailyVolumeLimitLiters {
			return domain.ErrCapacityExceeded
		}
		created, err = repository.InsertDischargeEvent(ctx, tx, event)
		if err != nil {
			return err
		}
		if !created {
			existing, err := s.store.ExistingDischargeByKey(ctx, tx, actor.OrganizationID, permit.ID, event.IdempotencyKey)
			if err != nil {
				return err
			}
			if existing.VolumeLiters != event.VolumeLiters || !existing.OccurredAt.Equal(event.OccurredAt) {
				return &domain.ConflictError{Resource: "idempotency key", Key: event.IdempotencyKey, Cause: errors.New("key was used for a different discharge")}
			}
			event = existing
			return nil
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "discharge.report", ObjectType: "discharge_event", ObjectID: event.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"permit_id":%q,"volume_liters":%d}`, permit.ID, event.VolumeLiters), OccurredAt: event.CreatedAt,
		})
	})
	if err != nil {
		return domain.DischargeEvent{}, false, fmt.Errorf("report discharge: %w", err)
	}
	return event, created, nil
}
