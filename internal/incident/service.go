package incident

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	lease time.Duration
}

type ReportCommand struct {
	SourceID    string                  `json:"source_id"`
	Title       string                  `json:"title"`
	Description string                  `json:"description"`
	Severity    domain.IncidentSeverity `json:"severity"`
	RequestID   string                  `json:"-"`
}

type AssignCommand struct {
	IncidentID     string
	LeaseToken     string
	ResourceCode   string
	AssigneeUserID string
	RequestID      string
}

func NewService(store *repository.Store, lease time.Duration) *Service {
	return &Service{store: store, clock: time.Now, lease: lease}
}

func (s *Service) Report(ctx context.Context, actor domain.Actor, command ReportCommand) (domain.Incident, error) {
	if !actor.CanCollect() && !actor.CanAnalyze() {
		return domain.Incident{}, domain.ErrForbidden
	}
	now := s.clock().UTC()
	incident := domain.Incident{
		ID: uuid.NewString(), OrganizationID: actor.OrganizationID, SourceID: command.SourceID,
		Title: strings.TrimSpace(command.Title), Description: strings.TrimSpace(command.Description),
		Severity: command.Severity, Status: domain.IncidentReported, Version: 1,
		ReportedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := incident.Validate(); err != nil {
		return domain.Incident{}, err
	}
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := s.store.WaterSource(ctx, tx, actor.OrganizationID, command.SourceID); err != nil {
			return err
		}
		if err := repository.InsertIncident(ctx, tx, incident); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"incident_id": incident.ID, "severity": incident.Severity})
		if err := repository.InsertOutboxEvent(ctx, tx, domain.OutboxEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, Topic: "incident.reported",
			AggregateType: "incident", AggregateID: incident.ID, IdempotencyKey: "manual-report:" + incident.ID,
			Payload: payload, Status: domain.OutboxPending, MaxAttempts: 5,
			AvailableAt: now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "incident.report", ObjectType: "incident", ObjectID: incident.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"severity":%q}`, incident.Severity), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.Incident{}, fmt.Errorf("report incident: %w", err)
	}
	return incident, nil
}

func (s *Service) Claim(ctx context.Context, actor domain.Actor, incidentID string) (domain.Incident, error) {
	if !actor.CanSupervise() {
		return domain.Incident{}, domain.ErrForbidden
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return domain.Incident{}, fmt.Errorf("generate incident lease token: %w", err)
	}
	incident, err := s.store.ClaimIncident(ctx, actor.OrganizationID, incidentID, actor.UserID, hex.EncodeToString(tokenBytes), s.clock().UTC(), s.lease)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("claim incident: %w", err)
	}
	return incident, nil
}

func (s *Service) Advance(ctx context.Context, actor domain.Actor, incidentID, leaseToken string, to domain.IncidentStatus, requestID string) error {
	if !actor.CanSupervise() {
		return domain.ErrForbidden
	}
	now := s.clock().UTC()
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		incident, err := s.store.Incident(ctx, tx, actor.OrganizationID, incidentID)
		if err != nil {
			return err
		}
		if err := incident.CanTransition(to); err != nil {
			return err
		}
		if to == domain.IncidentResolved {
			incomplete, err := s.store.CountIncompleteAssignments(ctx, tx, actor.OrganizationID, incident.ID)
			if err != nil {
				return err
			}
			if incomplete > 0 {
				return &domain.ConflictError{Resource: "incident", Key: incident.ID, Cause: errors.New("containment assignments are incomplete")}
			}
			openActions, err := s.store.CountOpenRemediationActions(ctx, tx, actor.OrganizationID, incident.ID)
			if err != nil {
				return err
			}
			if openActions > 0 {
				return &domain.ConflictError{Resource: "incident", Key: incident.ID, Cause: errors.New("remediation actions are incomplete")}
			}
		}
		if err := s.store.TransitionIncident(ctx, tx, incident, to, actor.UserID, leaseToken, now); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: requestID, Action: "incident.advance", ObjectType: "incident", ObjectID: incident.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"from":%q,"to":%q}`, incident.Status, to), OccurredAt: now,
		})
	})
	if err != nil {
		return fmt.Errorf("advance incident: %w", err)
	}
	return nil
}

func (s *Service) AssignContainment(ctx context.Context, actor domain.Actor, command AssignCommand) (domain.ContainmentAssignment, error) {
	if !actor.CanSupervise() {
		return domain.ContainmentAssignment{}, domain.ErrForbidden
	}
	now := s.clock().UTC()
	assignment := domain.ContainmentAssignment{
		ID: uuid.NewString(), IncidentID: command.IncidentID, OrganizationID: actor.OrganizationID,
		ResourceCode: strings.TrimSpace(command.ResourceCode), AssigneeUserID: command.AssigneeUserID,
		Status: domain.AssignmentPending, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if assignment.ResourceCode == "" || assignment.AssigneeUserID == "" {
		return domain.ContainmentAssignment{}, domain.NewValidationError("assign containment", domain.FieldViolation{Field: "assignment", Rule: "resource_code and assignee_user_id are required"})
	}
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		incident, err := s.store.Incident(ctx, tx, actor.OrganizationID, command.IncidentID)
		if err != nil {
			return err
		}
		if incident.CommanderUserID != actor.UserID || incident.LeaseToken != command.LeaseToken || incident.LeaseExpiresAt == nil || !incident.LeaseExpiresAt.After(now) {
			return &domain.LeaseError{Resource: "incident " + incident.ID, Owner: actor.UserID, Generation: incident.LeaseGeneration}
		}
		assignee, err := s.store.UserByID(ctx, tx, actor.OrganizationID, command.AssigneeUserID)
		if err != nil {
			return err
		}
		if !assignee.Active {
			return &domain.ConflictError{Resource: "containment assignee", Key: assignee.ID, Cause: errors.New("assignee is inactive")}
		}
		if err := repository.InsertContainmentAssignment(ctx, tx, assignment); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "containment.assign", ObjectType: "containment_assignment", ObjectID: assignment.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"incident_id":%q,"resource_code":%q}`, incident.ID, assignment.ResourceCode), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.ContainmentAssignment{}, fmt.Errorf("assign containment: %w", err)
	}
	return assignment, nil
}
