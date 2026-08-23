package domain

import (
	"strings"
	"time"
)

type IncidentStatus string

const (
	IncidentReported    IncidentStatus = "reported"
	IncidentAssessing   IncidentStatus = "assessing"
	IncidentContained   IncidentStatus = "contained"
	IncidentRemediating IncidentStatus = "remediating"
	IncidentResolved    IncidentStatus = "resolved"
)

type IncidentSeverity string

const (
	SeverityAdvisory    IncidentSeverity = "advisory"
	SeveritySignificant IncidentSeverity = "significant"
	SeverityCritical    IncidentSeverity = "critical"
)

type Incident struct {
	ID              string
	OrganizationID  string
	SourceID        string
	Title           string
	Description     string
	Severity        IncidentSeverity
	Status          IncidentStatus
	CommanderUserID string
	LeaseToken      string
	LeaseGeneration int64
	LeaseExpiresAt  *time.Time
	Version         int64
	ReportedAt      time.Time
	ResolvedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (i Incident) Validate() error {
	var violations []FieldViolation
	if i.OrganizationID == "" || i.SourceID == "" {
		violations = append(violations, FieldViolation{Field: "ownership", Rule: "organization and source are required"})
	}
	if len(strings.TrimSpace(i.Title)) < 5 {
		violations = append(violations, FieldViolation{Field: "title", Rule: "must contain at least 5 characters"})
	}
	if len(strings.TrimSpace(i.Description)) < 10 {
		violations = append(violations, FieldViolation{Field: "description", Rule: "must contain at least 10 characters"})
	}
	switch i.Severity {
	case SeverityAdvisory, SeveritySignificant, SeverityCritical:
	default:
		violations = append(violations, FieldViolation{Field: "severity", Rule: "is unsupported"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate incident", violations...)
	}
	return nil
}

func (i Incident) CanTransition(to IncidentStatus) error {
	allowed := map[IncidentStatus]map[IncidentStatus]bool{
		IncidentReported:    {IncidentAssessing: true},
		IncidentAssessing:   {IncidentContained: true},
		IncidentContained:   {IncidentRemediating: true},
		IncidentRemediating: {IncidentResolved: true},
		IncidentResolved:    {},
	}
	if !allowed[i.Status][to] {
		return &TransitionError{Entity: "incident", From: string(i.Status), To: string(to), Reason: "response phases must be completed in order"}
	}
	return nil
}

type AssignmentStatus string

const (
	AssignmentPending   AssignmentStatus = "pending"
	AssignmentActive    AssignmentStatus = "active"
	AssignmentCompleted AssignmentStatus = "completed"
	AssignmentCancelled AssignmentStatus = "cancelled"
)

type ContainmentAssignment struct {
	ID              string
	IncidentID      string
	OrganizationID  string
	ResourceCode    string
	AssigneeUserID  string
	Status          AssignmentStatus
	LeaseToken      string
	LeaseGeneration int64
	LeaseExpiresAt  *time.Time
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (a ContainmentAssignment) Terminal() bool {
	return a.Status == AssignmentCompleted || a.Status == AssignmentCancelled
}
