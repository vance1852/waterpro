package sampling

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

type CreatePlanCommand struct {
	SourceID        string    `json:"source_id"`
	StationID       string    `json:"station_id"`
	AssignedUserID  string    `json:"assigned_user_id"`
	WindowStart     time.Time `json:"window_start"`
	WindowEnd       time.Time `json:"window_end"`
	RequiredBottles int       `json:"required_bottles"`
	RequestID       string    `json:"-"`
}

type CollectCommand struct {
	PlanID      string    `json:"plan_id"`
	BottleCount int       `json:"bottle_count"`
	CollectedAt time.Time `json:"collected_at"`
	RequestID   string    `json:"-"`
}

type HandoffCommand struct {
	SampleID   string    `json:"sample_id"`
	ToUserID   string    `json:"to_user_id"`
	OccurredAt time.Time `json:"occurred_at"`
	RequestID  string    `json:"-"`
}

func NewService(store *repository.Store) *Service {
	return &Service{store: store, clock: time.Now}
}

func (s *Service) CreatePlan(ctx context.Context, actor domain.Actor, command CreatePlanCommand) (domain.SamplingPlan, error) {
	if !actor.CanSupervise() {
		return domain.SamplingPlan{}, domain.ErrForbidden
	}
	now := s.clock().UTC()
	plan := domain.SamplingPlan{
		ID: uuid.NewString(), OrganizationID: actor.OrganizationID, SourceID: command.SourceID,
		StationID: command.StationID, AssignedUserID: command.AssignedUserID,
		WindowStart: command.WindowStart.UTC(), WindowEnd: command.WindowEnd.UTC(),
		RequiredBottles: command.RequiredBottles, Status: domain.PlanDraft, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := plan.Validate(); err != nil {
		return domain.SamplingPlan{}, err
	}
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		station, err := s.store.MonitoringStation(ctx, tx, actor.OrganizationID, command.StationID)
		if err != nil {
			return err
		}
		if station.SourceID != command.SourceID || !station.Active {
			return &domain.ConflictError{Resource: "monitoring station", Key: station.ID, Cause: errors.New("station is inactive or belongs to another source")}
		}
		assignee, err := s.store.UserByID(ctx, tx, actor.OrganizationID, command.AssignedUserID)
		if err != nil {
			return err
		}
		if !assignee.Active || (assignee.Role != domain.RoleFieldOperator && assignee.Role != domain.RoleProtectionSupervisor) {
			return &domain.ConflictError{Resource: "sampling assignee", Key: assignee.ID, Cause: errors.New("assignee is not an active field operator")}
		}
		overlaps, err := s.store.CountOverlappingPlans(ctx, tx, station.ID, plan.WindowStart, plan.WindowEnd, "")
		if err != nil {
			return err
		}
		if overlaps >= 3 {
			return domain.ErrCapacityExceeded
		}
		if err := repository.InsertSamplingPlan(ctx, tx, plan); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "sampling_plan.create", ObjectType: "sampling_plan",
			ObjectID: plan.ID, Outcome: "success", Metadata: fmt.Sprintf(`{"station_id":%q,"assignee_id":%q}`, plan.StationID, plan.AssignedUserID), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.SamplingPlan{}, fmt.Errorf("create sampling plan: %w", err)
	}
	return plan, nil
}

func (s *Service) PublishPlan(ctx context.Context, actor domain.Actor, planID, requestID string) error {
	if !actor.CanSupervise() {
		return domain.ErrForbidden
	}
	now := s.clock().UTC()
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		plan, err := s.store.SamplingPlan(ctx, tx, actor.OrganizationID, planID)
		if err != nil {
			return err
		}
		if err := plan.CanTransition(domain.PlanPublished); err != nil {
			return err
		}
		if !plan.WindowEnd.After(now) {
			return &domain.TransitionError{Entity: "sampling plan", From: string(plan.Status), To: string(domain.PlanPublished), Reason: "sampling window has already ended"}
		}
		if err := s.store.TransitionSamplingPlan(ctx, tx, plan, domain.PlanPublished, now); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: requestID, Action: "sampling_plan.publish", ObjectType: "sampling_plan",
			ObjectID: plan.ID, Outcome: "success", Metadata: "{}", OccurredAt: now,
		})
	})
	if err != nil {
		return fmt.Errorf("publish sampling plan: %w", err)
	}
	return nil
}

func (s *Service) Collect(ctx context.Context, actor domain.Actor, command CollectCommand) (domain.Sample, error) {
	if !actor.CanCollect() {
		return domain.Sample{}, domain.ErrForbidden
	}
	now := s.clock().UTC()
	collectedAt := command.CollectedAt.UTC()
	if command.CollectedAt.IsZero() {
		collectedAt = now
	}
	var sample domain.Sample
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		plan, err := s.store.SamplingPlan(ctx, tx, actor.OrganizationID, command.PlanID)
		if err != nil {
			return err
		}
		if plan.Status != domain.PlanPublished {
			return &domain.TransitionError{Entity: "sampling plan", From: string(plan.Status), To: string(domain.PlanCompleted), Reason: "only published plans accept collection"}
		}
		if plan.AssignedUserID != actor.UserID && !actor.CanSupervise() {
			return domain.ErrForbidden
		}
		if collectedAt.Before(plan.WindowStart) || collectedAt.After(plan.WindowEnd) {
			return domain.NewValidationError("collect sample", domain.FieldViolation{Field: "collected_at", Rule: "must fall inside the published sampling window"})
		}
		if command.BottleCount != plan.RequiredBottles {
			return domain.NewValidationError("collect sample", domain.FieldViolation{Field: "bottle_count", Rule: "must match the plan requirement"})
		}
		station, err := s.store.MonitoringStation(ctx, tx, actor.OrganizationID, plan.StationID)
		if err != nil {
			return err
		}
		location, err := time.LoadLocation("UTC")
		if err != nil {
			return err
		}
		source, err := s.store.WaterSource(ctx, tx, actor.OrganizationID, plan.SourceID)
		if err != nil {
			return err
		}
		if source.Timezone != "" {
			location, err = time.LoadLocation(source.Timezone)
			if err != nil {
				return fmt.Errorf("load source timezone: %w", err)
			}
		}
		businessDay := collectedAt.In(location).Format("2006-01-02")
		sequence, err := s.store.NextSampleSequence(ctx, tx, actor.OrganizationID, station.ID, businessDay)
		if err != nil {
			return err
		}
		sample = domain.Sample{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, PlanID: plan.ID,
			StationID: station.ID, Sequence: sequence, Label: domain.FormatSampleLabel(station.Code, collectedAt.In(location), sequence),
			BottleCount: command.BottleCount, Status: domain.SampleCollected, CustodianUserID: actor.UserID,
			CollectedAt: &collectedAt, Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := sample.Validate(); err != nil {
			return err
		}
		if err := repository.InsertSample(ctx, tx, sample); err != nil {
			return err
		}
		event := domain.CustodyEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, SampleID: sample.ID,
			ToUserID: actor.UserID, Action: "collected", OccurredAt: collectedAt, RequestID: command.RequestID,
		}
		if err := repository.InsertCustodyEvent(ctx, tx, event); err != nil {
			return err
		}
		if err := s.store.TransitionSamplingPlan(ctx, tx, plan, domain.PlanCompleted, now); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "sample.collect", ObjectType: "sample", ObjectID: sample.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"label":%q,"plan_id":%q}`, sample.Label, sample.PlanID), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.Sample{}, fmt.Errorf("collect sample: %w", err)
	}
	return sample, nil
}

func (s *Service) Handoff(ctx context.Context, actor domain.Actor, command HandoffCommand) (domain.Sample, error) {
	if !actor.CanCollect() && !actor.CanAnalyze() {
		return domain.Sample{}, domain.ErrForbidden
	}
	when := command.OccurredAt.UTC()
	if command.OccurredAt.IsZero() {
		when = s.clock().UTC()
	}
	var updated domain.Sample
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		sample, err := s.store.Sample(ctx, tx, actor.OrganizationID, command.SampleID)
		if err != nil {
			return err
		}
		if sample.CustodianUserID != actor.UserID && !actor.CanSupervise() {
			return domain.ErrForbidden
		}
		receiver, err := s.store.UserByID(ctx, tx, actor.OrganizationID, command.ToUserID)
		if err != nil {
			return err
		}
		if !receiver.Active {
			return &domain.ConflictError{Resource: "custody receiver", Key: receiver.ID, Cause: errors.New("receiver is inactive")}
		}
		var next domain.SampleStatus
		switch sample.Status {
		case domain.SampleCollected:
			next = domain.SampleInTransit
		case domain.SampleInTransit:
			next = domain.SampleReceived
		default:
			return &domain.TransitionError{Entity: "sample", From: string(sample.Status), To: "handoff", Reason: "sample is not in a handoff state"}
		}
		if err := sample.CanTransition(next); err != nil {
			return err
		}
		if err := s.store.TransitionSample(ctx, tx, sample, next, receiver.ID, when); err != nil {
			return err
		}
		event := domain.CustodyEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, SampleID: sample.ID,
			FromUserID: sample.CustodianUserID, ToUserID: receiver.ID, Action: string(next), OccurredAt: when, RequestID: command.RequestID,
		}
		if err := event.Validate(); err != nil {
			return err
		}
		if err := repository.InsertCustodyEvent(ctx, tx, event); err != nil {
			return err
		}
		if err := audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "sample.handoff", ObjectType: "sample", ObjectID: sample.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"from":%q,"to":%q,"status":%q}`, sample.CustodianUserID, receiver.ID, next), OccurredAt: when,
		}); err != nil {
			return err
		}
		sample.Status = next
		sample.CustodianUserID = receiver.ID
		sample.Version++
		if next == domain.SampleReceived {
			sample.ReceivedAt = &when
		}
		updated = sample
		return nil
	})
	if err != nil {
		return domain.Sample{}, fmt.Errorf("handoff sample: %w", err)
	}
	return updated, nil
}

func (s *Service) CustodyHistory(ctx context.Context, actor domain.Actor, sampleID string) ([]domain.CustodyEvent, error) {
	if _, err := s.store.Sample(ctx, s.store.DB(), actor.OrganizationID, sampleID); err != nil {
		return nil, err
	}
	events, err := s.store.ListCustodyEvents(ctx, actor.OrganizationID, sampleID)
	if err != nil {
		return nil, fmt.Errorf("list custody history: %w", err)
	}
	result := make([]domain.CustodyEvent, len(events))
	copy(result, events)
	return result, nil
}
