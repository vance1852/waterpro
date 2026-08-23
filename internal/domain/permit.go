package domain

import (
	"strings"
	"time"
)

type PermitStatus string

const (
	PermitDraft     PermitStatus = "draft"
	PermitActive    PermitStatus = "active"
	PermitSuspended PermitStatus = "suspended"
	PermitExpired   PermitStatus = "expired"
	PermitRevoked   PermitStatus = "revoked"
)

type Permit struct {
	ID                     string
	OrganizationID         string
	SourceID               string
	HolderName             string
	Reference              string
	ValidFrom              time.Time
	ValidUntil             time.Time
	DailyVolumeLimitLiters int64
	Status                 PermitStatus
	Version                int64
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (p Permit) Validate() error {
	var violations []FieldViolation
	if p.OrganizationID == "" || p.SourceID == "" {
		violations = append(violations, FieldViolation{Field: "ownership", Rule: "organization and source are required"})
	}
	if strings.TrimSpace(p.HolderName) == "" || strings.TrimSpace(p.Reference) == "" {
		violations = append(violations, FieldViolation{Field: "identity", Rule: "holder_name and reference are required"})
	}
	if !p.ValidUntil.After(p.ValidFrom) {
		violations = append(violations, FieldViolation{Field: "valid_until", Rule: "must be after valid_from"})
	}
	if p.DailyVolumeLimitLiters <= 0 {
		violations = append(violations, FieldViolation{Field: "daily_volume_limit_liters", Rule: "must be positive"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate permit", violations...)
	}
	return nil
}

func (p Permit) CanTransition(to PermitStatus, now time.Time) error {
	allowed := map[PermitStatus]map[PermitStatus]bool{
		PermitDraft:     {PermitActive: true, PermitRevoked: true},
		PermitActive:    {PermitSuspended: true, PermitExpired: true, PermitRevoked: true},
		PermitSuspended: {PermitActive: true, PermitExpired: true, PermitRevoked: true},
		PermitExpired:   {},
		PermitRevoked:   {},
	}
	if !allowed[p.Status][to] {
		return &TransitionError{Entity: "permit", From: string(p.Status), To: string(to), Reason: "transition is not permitted"}
	}
	if to == PermitActive && (now.Before(p.ValidFrom) || !now.Before(p.ValidUntil)) {
		return &TransitionError{Entity: "permit", From: string(p.Status), To: string(to), Reason: "permit is outside its validity window"}
	}
	return nil
}

type DischargeEvent struct {
	ID             string
	OrganizationID string
	PermitID       string
	IdempotencyKey string
	VolumeLiters   int64
	OccurredAt     time.Time
	ReportedBy     string
	CreatedAt      time.Time
}

func (e DischargeEvent) Validate() error {
	var violations []FieldViolation
	if e.OrganizationID == "" || e.PermitID == "" || e.ReportedBy == "" {
		violations = append(violations, FieldViolation{Field: "ownership", Rule: "organization, permit and reporter are required"})
	}
	if strings.TrimSpace(e.IdempotencyKey) == "" {
		violations = append(violations, FieldViolation{Field: "idempotency_key", Rule: "is required"})
	}
	if e.VolumeLiters <= 0 {
		violations = append(violations, FieldViolation{Field: "volume_liters", Rule: "must be positive"})
	}
	if e.OccurredAt.IsZero() {
		violations = append(violations, FieldViolation{Field: "occurred_at", Rule: "is required"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate discharge event", violations...)
	}
	return nil
}
