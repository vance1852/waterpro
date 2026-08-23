package remediation

import (
	"context"
	"database/sql"
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

type CreatePlanCommand struct {
	IncidentID  string
	Title       string
	Objective   string
	BudgetCents int64
	Actions     []CreateAction
	RequestID   string
}

type CreateAction struct {
	IdempotencyKey string
	Description    string
}

type CompleteActionCommand struct {
	PlanID          string
	ActionID        string
	ExpectedVersion int64
	Success         bool
	FailureReason   string
	RequestID       string
}

func NewService(store *repository.Store) *Service {
	return &Service{store: store, clock: time.Now}
}

func (s *Service) CreatePlan(ctx context.Context, actor domain.Actor, command CreatePlanCommand) (domain.RemediationPlan, error) {
	if !actor.CanSupervise() {
		return domain.RemediationPlan{}, domain.ErrForbidden
	}
	if len(command.Actions) == 0 || len(command.Actions) > 50 {
		return domain.RemediationPlan{}, domain.NewValidationError("create remediation plan", domain.FieldViolation{Field: "actions", Rule: "must contain between 1 and 50 actions"})
	}
	now := s.clock().UTC()
	plan := domain.RemediationPlan{
		ID: uuid.NewString(), OrganizationID: actor.OrganizationID, IncidentID: command.IncidentID,
		Title: strings.TrimSpace(command.Title), Objective: strings.TrimSpace(command.Objective),
		BudgetCents: command.BudgetCents, Status: domain.RemediationDraft,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := plan.Validate(); err != nil {
		return domain.RemediationPlan{}, err
	}
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		incident, err := s.store.Incident(ctx, tx, actor.OrganizationID, command.IncidentID)
		if err != nil {
			return err
		}
		if incident.Status == domain.IncidentResolved {
			return &domain.ConflictError{Resource: "incident", Key: incident.ID, Cause: errors.New("resolved incident cannot receive a remediation plan")}
		}
		if err := repository.InsertRemediationPlan(ctx, tx, plan); err != nil {
			return err
		}
		keys := make(map[string]struct{}, len(command.Actions))
		for _, input := range command.Actions {
			key := strings.TrimSpace(input.IdempotencyKey)
			description := strings.TrimSpace(input.Description)
			if key == "" || len(description) < 5 {
				return domain.NewValidationError("create remediation action", domain.FieldViolation{Field: "action", Rule: "idempotency key and meaningful description are required"})
			}
			if _, exists := keys[key]; exists {
				return &domain.ConflictError{Resource: "remediation action key", Key: key, Cause: errors.New("duplicate key within plan")}
			}
			keys[key] = struct{}{}
			action := domain.RemediationAction{
				ID: uuid.NewString(), PlanID: plan.ID, OrganizationID: actor.OrganizationID,
				IdempotencyKey: key, Description: description, Status: domain.ActionPending,
				Version: 1, CreatedAt: now, UpdatedAt: now,
			}
			if err := repository.InsertRemediationAction(ctx, tx, action); err != nil {
				return err
			}
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "remediation_plan.create", ObjectType: "remediation_plan", ObjectID: plan.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"incident_id":%q,"action_count":%d}`, plan.IncidentID, len(command.Actions)), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.RemediationPlan{}, fmt.Errorf("create remediation plan: %w", err)
	}
	return plan, nil
}

func (s *Service) Approve(ctx context.Context, actor domain.Actor, planID, requestID string) error {
	if !actor.CanSupervise() {
		return domain.ErrForbidden
	}
	now := s.clock().UTC()
	plan, err := s.store.RemediationPlan(ctx, s.store.DB(), actor.OrganizationID, planID)
	if err != nil {
		return fmt.Errorf("load remediation plan for approval: %w", err)
	}
	err = s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := plan.CanTransition(domain.RemediationApproved); err != nil {
			return err
		}
		counts, err := s.store.CountActionsByStatus(ctx, tx, actor.OrganizationID, plan.ID)
		if err != nil {
			return err
		}
		if counts[domain.ActionPending] == 0 {
			return &domain.ConflictError{Resource: "remediation plan", Key: plan.ID, Cause: errors.New("plan has no pending actions")}
		}
		if err := s.store.TransitionRemediationPlan(ctx, tx, plan, domain.RemediationApproved, actor.UserID, now); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: requestID, Action: "remediation_plan.approve", ObjectType: "remediation_plan", ObjectID: plan.ID,
			Outcome: "success", Metadata: "{}", OccurredAt: now,
		})
	})
	if err != nil {
		return fmt.Errorf("approve remediation plan: %w", err)
	}
	return nil
}

func (s *Service) CompleteAction(ctx context.Context, actor domain.Actor, command CompleteActionCommand) error {
	if !actor.CanCollect() && !actor.CanSupervise() {
		return domain.ErrForbidden
	}
	if !command.Success && len(strings.TrimSpace(command.FailureReason)) < 5 {
		return domain.NewValidationError("complete remediation action", domain.FieldViolation{Field: "failure_reason", Rule: "is required for failed actions"})
	}
	now := s.clock().UTC()
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		plan, err := s.store.RemediationPlan(ctx, tx, actor.OrganizationID, command.PlanID)
		if err != nil {
			return err
		}
		if plan.Status != domain.RemediationApproved && plan.Status != domain.RemediationExecuting {
			return &domain.TransitionError{Entity: "remediation plan", From: string(plan.Status), To: string(domain.RemediationExecuting), Reason: "plan is not approved for execution"}
		}
		if err := s.store.CompleteRemediationAction(ctx, tx, actor.OrganizationID, command.ActionID, command.ExpectedVersion, command.Success, strings.TrimSpace(command.FailureReason), now); err != nil {
			return err
		}
		if plan.Status == domain.RemediationApproved {
			if err := s.store.TransitionRemediationPlan(ctx, tx, plan, domain.RemediationExecuting, "", now); err != nil {
				return err
			}
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "remediation_action.complete", ObjectType: "remediation_action", ObjectID: command.ActionID,
			Outcome: map[bool]string{true: "success", false: "failed"}[command.Success], Metadata: fmt.Sprintf(`{"plan_id":%q}`, plan.ID), OccurredAt: now,
		})
	})
	if err != nil {
		return fmt.Errorf("complete remediation action: %w", err)
	}
	return nil
}
