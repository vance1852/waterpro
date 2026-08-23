package domain

import (
	"math"
	"strings"
	"time"
)

type TelemetryReading struct {
	ID             string
	OrganizationID string
	StationID      string
	ExternalID     string
	Parameter      string
	Value          float64
	Unit           string
	Threshold      float64
	ObservedAt     time.Time
	ReceivedAt     time.Time
}

func (r TelemetryReading) Validate(now time.Time) error {
	var violations []FieldViolation
	if r.OrganizationID == "" || r.StationID == "" {
		violations = append(violations, FieldViolation{Field: "ownership", Rule: "organization and station are required"})
	}
	if strings.TrimSpace(r.ExternalID) == "" {
		violations = append(violations, FieldViolation{Field: "external_id", Rule: "is required"})
	}
	if strings.TrimSpace(r.Parameter) == "" || strings.TrimSpace(r.Unit) == "" {
		violations = append(violations, FieldViolation{Field: "measurement", Rule: "parameter and unit are required"})
	}
	if math.IsNaN(r.Value) || math.IsInf(r.Value, 0) {
		violations = append(violations, FieldViolation{Field: "value", Rule: "must be finite"})
	}
	if r.ObservedAt.IsZero() || r.ObservedAt.After(now.Add(5*time.Minute)) {
		violations = append(violations, FieldViolation{Field: "observed_at", Rule: "must not be empty or far in the future"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate telemetry reading", violations...)
	}
	return nil
}

func (r TelemetryReading) ExceedsThreshold() bool { return r.Value > r.Threshold }

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobRetry     JobStatus = "retry"
	JobSucceeded JobStatus = "succeeded"
	JobDead      JobStatus = "dead"
)

type AlertJob struct {
	ID              string
	OrganizationID  string
	ReadingID       string
	Status          JobStatus
	AttemptCount    int
	MaxAttempts     int
	AvailableAt     time.Time
	LeaseOwner      string
	LeaseToken      string
	LeaseGeneration int64
	LeaseExpiresAt  *time.Time
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (j AlertJob) CanRetry() bool { return j.AttemptCount < j.MaxAttempts }

func RetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Second * time.Duration(1<<(attempt-1))
}
