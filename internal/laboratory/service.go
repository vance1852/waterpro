package laboratory

import (
	"context"
	"database/sql"
	"encoding/json"
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

type RecordResultCommand struct {
	SampleID        string
	Parameter       string
	Value           float64
	Unit            string
	MethodCode      string
	DetectionLimit  float64
	RegulatoryLimit float64
	MeasuredAt      time.Time
	RequestID       string
}

func NewService(store *repository.Store) *Service {
	return &Service{store: store, clock: time.Now}
}

func (s *Service) RecordResult(ctx context.Context, actor domain.Actor, command RecordResultCommand) (domain.LabResult, error) {
	if !actor.CanAnalyze() {
		return domain.LabResult{}, domain.ErrForbidden
	}
	now := s.clock().UTC()
	result := domain.LabResult{
		ID: uuid.NewString(), OrganizationID: actor.OrganizationID, SampleID: command.SampleID,
		Parameter: strings.TrimSpace(command.Parameter), Value: command.Value, Unit: strings.TrimSpace(command.Unit),
		MethodCode: strings.TrimSpace(command.MethodCode), DetectionLimit: command.DetectionLimit,
		RegulatoryLimit: command.RegulatoryLimit, Status: domain.LabResultDraft, AnalystUserID: actor.UserID,
		Version: 1, MeasuredAt: command.MeasuredAt.UTC(), CreatedAt: now, UpdatedAt: now,
	}
	if err := result.Validate(); err != nil {
		return domain.LabResult{}, err
	}
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		sample, err := s.store.Sample(ctx, tx, actor.OrganizationID, command.SampleID)
		if err != nil {
			return err
		}
		if sample.Status != domain.SampleReceived {
			return &domain.TransitionError{Entity: "sample", From: string(sample.Status), To: string(domain.SampleTested), Reason: "sample must be received before analysis"}
		}
		if sample.CustodianUserID != actor.UserID && !actor.CanSupervise() {
			return domain.ErrForbidden
		}
		if err := repository.InsertLabResult(ctx, tx, result); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: command.RequestID, Action: "lab_result.record", ObjectType: "lab_result", ObjectID: result.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"sample_id":%q,"parameter":%q}`, result.SampleID, result.Parameter), OccurredAt: now,
		})
	})
	if err != nil {
		return domain.LabResult{}, fmt.Errorf("record laboratory result: %w", err)
	}
	return result, nil
}

func (s *Service) Submit(ctx context.Context, actor domain.Actor, resultID, requestID string) error {
	if !actor.CanAnalyze() {
		return domain.ErrForbidden
	}
	now := s.clock().UTC()
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, err := s.store.LabResult(ctx, tx, actor.OrganizationID, resultID)
		if err != nil {
			return err
		}
		if result.AnalystUserID != actor.UserID && !actor.CanSupervise() {
			return domain.ErrForbidden
		}
		if err := result.CanTransition(domain.LabResultSubmitted, ""); err != nil {
			return err
		}
		if err := s.store.TransitionLabResult(ctx, tx, result, domain.LabResultSubmitted, "", now); err != nil {
			return err
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: requestID, Action: "lab_result.submit", ObjectType: "lab_result", ObjectID: result.ID,
			Outcome: "success", Metadata: "{}", OccurredAt: now,
		})
	})
	if err != nil {
		return fmt.Errorf("submit laboratory result: %w", err)
	}
	return nil
}

func (s *Service) Review(ctx context.Context, actor domain.Actor, resultID string, approve bool, requestID string) (string, error) {
	if !actor.CanAnalyze() {
		return "", domain.ErrForbidden
	}
	now := s.clock().UTC()
	var incidentID string
	err := s.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, err := s.store.LabResult(ctx, tx, actor.OrganizationID, resultID)
		if err != nil {
			return err
		}
		to := domain.LabResultRejected
		if approve {
			to = domain.LabResultApproved
		}
		if err := result.CanTransition(to, actor.UserID); err != nil {
			return err
		}
		if err := s.store.TransitionLabResult(ctx, tx, result, to, actor.UserID, now); err != nil {
			return err
		}
		if approve {
			sample, err := s.store.Sample(ctx, tx, actor.OrganizationID, result.SampleID)
			if err != nil {
				return err
			}
			if err := sample.CanTransition(domain.SampleTested); err != nil {
				return err
			}
			if err := s.store.TransitionSample(ctx, tx, sample, domain.SampleTested, sample.CustodianUserID, now); err != nil {
				return err
			}
			if result.ExceedsLimit() {
				incidentID = uuid.NewString()
				severity := domain.SeveritySignificant
				if result.Value >= result.RegulatoryLimit*2 {
					severity = domain.SeverityCritical
				}
				_, err := tx.ExecContext(ctx, `
					INSERT INTO incidents(
						id, organization_id, source_id, originating_result_id, title, description,
						severity, status, lease_generation, version, reported_at, created_at, updated_at
					)
					SELECT ?, ?, sp.source_id, ?, ?, ?, ?, 'reported', 0, 1, ?, ?, ?
					FROM samples s JOIN sampling_plans sp ON sp.id = s.plan_id
					WHERE s.id = ? AND s.organization_id = ?`,
					incidentID, actor.OrganizationID, result.ID,
					"Confirmed water quality exceedance", fmt.Sprintf("%s exceeded its regulatory limit", result.Parameter),
					string(severity), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), sample.ID, actor.OrganizationID)
				if err != nil {
					return fmt.Errorf("create exceedance incident: %w", err)
				}
				payload, _ := json.Marshal(map[string]any{"incident_id": incidentID, "result_id": result.ID, "severity": severity})
				outbox := domain.OutboxEvent{
					ID: uuid.NewString(), OrganizationID: actor.OrganizationID, Topic: "incident.reported",
					AggregateType: "incident", AggregateID: incidentID, IdempotencyKey: "lab-exceedance:" + result.ID,
					Payload: payload, Status: domain.OutboxPending, MaxAttempts: 5, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
				}
				if err := repository.InsertOutboxEvent(ctx, tx, outbox); err != nil {
					return err
				}
			}
		}
		return audit.Insert(ctx, tx, domain.AuditEvent{
			ID: uuid.NewString(), OrganizationID: actor.OrganizationID, ActorUserID: actor.UserID,
			RequestID: requestID, Action: "lab_result.review", ObjectType: "lab_result", ObjectID: result.ID,
			Outcome: "success", Metadata: fmt.Sprintf(`{"decision":%q,"incident_id":%q}`, to, incidentID), OccurredAt: now,
		})
	})
	if err != nil {
		return "", fmt.Errorf("review laboratory result: %w", err)
	}
	return incidentID, nil
}
