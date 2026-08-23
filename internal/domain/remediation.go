package domain

import (
	"strings"
	"time"
)

type RemediationStatus string

const (
	RemediationDraft     RemediationStatus = "draft"
	RemediationApproved  RemediationStatus = "approved"
	RemediationExecuting RemediationStatus = "executing"
	RemediationVerified  RemediationStatus = "verified"
	RemediationClosed    RemediationStatus = "closed"
)

type RemediationPlan struct {
	ID             string
	OrganizationID string
	IncidentID     string
	Title          string
	Objective      string
	BudgetCents    int64
	Status         RemediationStatus
	ApprovedBy     string
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (p RemediationPlan) Validate() error {
	var violations []FieldViolation
	if p.OrganizationID == "" || p.IncidentID == "" {
		violations = append(violations, FieldViolation{Field: "ownership", Rule: "organization and incident are required"})
	}
	if len(strings.TrimSpace(p.Title)) < 5 {
		violations = append(violations, FieldViolation{Field: "title", Rule: "must contain at least 5 characters"})
	}
	if len(strings.TrimSpace(p.Objective)) < 10 {
		violations = append(violations, FieldViolation{Field: "objective", Rule: "must contain at least 10 characters"})
	}
	if p.BudgetCents <= 0 {
		violations = append(violations, FieldViolation{Field: "budget_cents", Rule: "must be positive"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate remediation plan", violations...)
	}
	return nil
}

func (p RemediationPlan) CanTransition(to RemediationStatus) error {
	allowed := map[RemediationStatus]map[RemediationStatus]bool{
		RemediationDraft:     {RemediationApproved: true},
		RemediationApproved:  {RemediationExecuting: true},
		RemediationExecuting: {RemediationVerified: true},
		RemediationVerified:  {RemediationClosed: true, RemediationExecuting: true},
		RemediationClosed:    {},
	}
	if !allowed[p.Status][to] {
		return &TransitionError{Entity: "remediation plan", From: string(p.Status), To: string(to), Reason: "transition is not permitted"}
	}
	return nil
}

type ActionStatus string

const (
	ActionPending    ActionStatus = "pending"
	ActionInProgress ActionStatus = "in_progress"
	ActionSucceeded  ActionStatus = "succeeded"
	ActionFailed     ActionStatus = "failed"
)

type RemediationAction struct {
	ID             string
	PlanID         string
	OrganizationID string
	IdempotencyKey string
	Description    string
	Status         ActionStatus
	AttemptCount   int
	LastError      string
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type BatchActionResult struct {
	ActionID  string       `json:"action_id"`
	Status    ActionStatus `json:"status"`
	ErrorCode string       `json:"error_code,omitempty"`
}
