package domain

import (
	"fmt"
	"strings"
	"time"
)

type SamplingPlanStatus string

const (
	PlanDraft     SamplingPlanStatus = "draft"
	PlanPublished SamplingPlanStatus = "published"
	PlanCompleted SamplingPlanStatus = "completed"
	PlanCancelled SamplingPlanStatus = "cancelled"
)

type SamplingPlan struct {
	ID              string
	OrganizationID  string
	SourceID        string
	StationID       string
	AssignedUserID  string
	WindowStart     time.Time
	WindowEnd       time.Time
	RequiredBottles int
	Status          SamplingPlanStatus
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (p SamplingPlan) Validate() error {
	var violations []FieldViolation
	if p.OrganizationID == "" || p.SourceID == "" || p.StationID == "" {
		violations = append(violations, FieldViolation{Field: "ownership", Rule: "organization, source and station are required"})
	}
	if p.AssignedUserID == "" {
		violations = append(violations, FieldViolation{Field: "assigned_user_id", Rule: "is required"})
	}
	if !p.WindowEnd.After(p.WindowStart) {
		violations = append(violations, FieldViolation{Field: "window_end", Rule: "must be after window_start"})
	}
	if p.RequiredBottles < 1 || p.RequiredBottles > 24 {
		violations = append(violations, FieldViolation{Field: "required_bottles", Rule: "must be between 1 and 24"})
	}
	if p.Status == "" {
		p.Status = PlanDraft
	}
	switch p.Status {
	case PlanDraft, PlanPublished, PlanCompleted, PlanCancelled:
	default:
		violations = append(violations, FieldViolation{Field: "status", Rule: "is unsupported"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate sampling plan", violations...)
	}
	return nil
}

func (p SamplingPlan) CanTransition(to SamplingPlanStatus) error {
	allowed := map[SamplingPlanStatus]map[SamplingPlanStatus]bool{
		PlanDraft:     {PlanPublished: true, PlanCancelled: true},
		PlanPublished: {PlanCompleted: true, PlanCancelled: true},
		PlanCompleted: {},
		PlanCancelled: {},
	}
	if !allowed[p.Status][to] {
		return &TransitionError{Entity: "sampling plan", From: string(p.Status), To: string(to), Reason: "transition is not permitted"}
	}
	return nil
}

type SampleStatus string

const (
	SamplePlanned   SampleStatus = "planned"
	SampleCollected SampleStatus = "collected"
	SampleInTransit SampleStatus = "in_transit"
	SampleReceived  SampleStatus = "received"
	SampleTested    SampleStatus = "tested"
	SampleArchived  SampleStatus = "archived"
	SampleRejected  SampleStatus = "rejected"
)

type Sample struct {
	ID              string
	OrganizationID  string
	PlanID          string
	StationID       string
	Sequence        int64
	Label           string
	BottleCount     int
	Status          SampleStatus
	CustodianUserID string
	CollectedAt     *time.Time
	ReceivedAt      *time.Time
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (s Sample) Validate() error {
	var violations []FieldViolation
	if s.OrganizationID == "" || s.PlanID == "" || s.StationID == "" {
		violations = append(violations, FieldViolation{Field: "ownership", Rule: "organization, plan and station are required"})
	}
	if s.Sequence < 1 {
		violations = append(violations, FieldViolation{Field: "sequence", Rule: "must be positive"})
	}
	if strings.TrimSpace(s.Label) == "" {
		violations = append(violations, FieldViolation{Field: "label", Rule: "is required"})
	}
	if s.BottleCount < 1 {
		violations = append(violations, FieldViolation{Field: "bottle_count", Rule: "must be positive"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate sample", violations...)
	}
	return nil
}

func (s Sample) CanTransition(to SampleStatus) error {
	allowed := map[SampleStatus]map[SampleStatus]bool{
		SamplePlanned:   {SampleCollected: true, SampleRejected: true},
		SampleCollected: {SampleInTransit: true, SampleRejected: true},
		SampleInTransit: {SampleReceived: true, SampleRejected: true},
		SampleReceived:  {SampleTested: true, SampleRejected: true},
		SampleTested:    {SampleArchived: true},
		SampleArchived:  {},
		SampleRejected:  {},
	}
	if !allowed[s.Status][to] {
		return &TransitionError{Entity: "sample", From: string(s.Status), To: string(to), Reason: "chain of custody would be broken"}
	}
	return nil
}

func FormatSampleLabel(stationCode string, day time.Time, sequence int64) string {
	return fmt.Sprintf("%s-%s-%04d", strings.ToUpper(stationCode), day.Format("20060102"), sequence)
}

type CustodyEvent struct {
	ID             string
	OrganizationID string
	SampleID       string
	FromUserID     string
	ToUserID       string
	Action         string
	OccurredAt     time.Time
	RequestID      string
}

func (e CustodyEvent) Validate() error {
	var violations []FieldViolation
	if e.SampleID == "" || e.ToUserID == "" {
		violations = append(violations, FieldViolation{Field: "custody", Rule: "sample and receiving user are required"})
	}
	if strings.TrimSpace(e.Action) == "" {
		violations = append(violations, FieldViolation{Field: "action", Rule: "is required"})
	}
	if e.OccurredAt.IsZero() {
		violations = append(violations, FieldViolation{Field: "occurred_at", Rule: "is required"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate custody event", violations...)
	}
	return nil
}
