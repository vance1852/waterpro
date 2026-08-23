package domain

import (
	"math"
	"strings"
	"time"
)

type LabResultStatus string

const (
	LabResultDraft     LabResultStatus = "draft"
	LabResultSubmitted LabResultStatus = "submitted"
	LabResultApproved  LabResultStatus = "approved"
	LabResultRejected  LabResultStatus = "rejected"
)

type LabResult struct {
	ID              string
	OrganizationID  string
	SampleID        string
	Parameter       string
	Value           float64
	Unit            string
	MethodCode      string
	DetectionLimit  float64
	RegulatoryLimit float64
	Status          LabResultStatus
	AnalystUserID   string
	ReviewerUserID  string
	Version         int64
	MeasuredAt      time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (r LabResult) Validate() error {
	var violations []FieldViolation
	if r.OrganizationID == "" || r.SampleID == "" {
		violations = append(violations, FieldViolation{Field: "ownership", Rule: "organization and sample are required"})
	}
	if strings.TrimSpace(r.Parameter) == "" {
		violations = append(violations, FieldViolation{Field: "parameter", Rule: "is required"})
	}
	if math.IsNaN(r.Value) || math.IsInf(r.Value, 0) || r.Value < 0 {
		violations = append(violations, FieldViolation{Field: "value", Rule: "must be finite and non-negative"})
	}
	if strings.TrimSpace(r.Unit) == "" || strings.TrimSpace(r.MethodCode) == "" {
		violations = append(violations, FieldViolation{Field: "method", Rule: "unit and method_code are required"})
	}
	if r.DetectionLimit < 0 || r.RegulatoryLimit <= 0 {
		violations = append(violations, FieldViolation{Field: "limits", Rule: "detection limit must be non-negative and regulatory limit positive"})
	}
	if r.MeasuredAt.IsZero() {
		violations = append(violations, FieldViolation{Field: "measured_at", Rule: "is required"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate laboratory result", violations...)
	}
	return nil
}

func (r LabResult) ExceedsLimit() bool { return r.Value > r.RegulatoryLimit }

func (r LabResult) CanTransition(to LabResultStatus, reviewer string) error {
	allowed := map[LabResultStatus]map[LabResultStatus]bool{
		LabResultDraft:     {LabResultSubmitted: true},
		LabResultSubmitted: {LabResultApproved: true, LabResultRejected: true},
		LabResultApproved:  {},
		LabResultRejected:  {LabResultDraft: true},
	}
	if !allowed[r.Status][to] {
		return &TransitionError{Entity: "laboratory result", From: string(r.Status), To: string(to), Reason: "transition is not permitted"}
	}
	if (to == LabResultApproved || to == LabResultRejected) && reviewer == r.AnalystUserID {
		return NewValidationError("review laboratory result", FieldViolation{Field: "reviewer", Rule: "must differ from analyst"})
	}
	return nil
}
