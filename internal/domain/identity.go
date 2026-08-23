package domain

import (
	"strings"
	"time"
)

type Role string

const (
	RoleFieldOperator        Role = "field_operator"
	RoleLabAnalyst           Role = "lab_analyst"
	RoleProtectionSupervisor Role = "protection_supervisor"
)

func (r Role) Valid() bool {
	switch r {
	case RoleFieldOperator, RoleLabAnalyst, RoleProtectionSupervisor:
		return true
	default:
		return false
	}
}

type Actor struct {
	UserID         string
	OrganizationID string
	Role           Role
	AuthGeneration int64
}

func (a Actor) Validate() error {
	var violations []FieldViolation
	if strings.TrimSpace(a.UserID) == "" {
		violations = append(violations, FieldViolation{Field: "user_id", Rule: "is required"})
	}
	if strings.TrimSpace(a.OrganizationID) == "" {
		violations = append(violations, FieldViolation{Field: "organization_id", Rule: "is required"})
	}
	if !a.Role.Valid() {
		violations = append(violations, FieldViolation{Field: "role", Rule: "is unsupported"})
	}
	if a.AuthGeneration < 1 {
		violations = append(violations, FieldViolation{Field: "auth_generation", Rule: "must be positive"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate actor", violations...)
	}
	return nil
}

func (a Actor) CanSupervise() bool { return a.Role == RoleProtectionSupervisor }
func (a Actor) CanAnalyze() bool {
	return a.Role == RoleLabAnalyst || a.Role == RoleProtectionSupervisor
}
func (a Actor) CanCollect() bool {
	return a.Role == RoleFieldOperator || a.Role == RoleProtectionSupervisor
}

type User struct {
	ID               string
	OrganizationID   string
	Email            string
	PasswordHash     string
	Role             Role
	Active           bool
	AuthGeneration   int64
	FailedLoginCount int
	LockedUntil      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Session struct {
	ID             string
	UserID         string
	TokenHash      string
	AuthGeneration int64
	ExpiresAt      time.Time
	RevokedAt      *time.Time
	CreatedAt      time.Time
}

func (s Session) ActiveAt(now time.Time, user User) bool {
	return s.RevokedAt == nil && s.ExpiresAt.After(now) && user.Active && s.AuthGeneration == user.AuthGeneration
}
