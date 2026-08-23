package domain

import (
	"strings"
	"time"
)

type SourceKind string

const (
	SourceReservoir   SourceKind = "reservoir"
	SourceRiverIntake SourceKind = "river_intake"
	SourceGroundwater SourceKind = "groundwater"
)

type WaterSource struct {
	ID             string
	OrganizationID string
	Name           string
	Kind           SourceKind
	Timezone       string
	Active         bool
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (s WaterSource) Validate() error {
	var violations []FieldViolation
	if strings.TrimSpace(s.OrganizationID) == "" {
		violations = append(violations, FieldViolation{Field: "organization_id", Rule: "is required"})
	}
	if len(strings.TrimSpace(s.Name)) < 3 {
		violations = append(violations, FieldViolation{Field: "name", Rule: "must contain at least 3 characters"})
	}
	switch s.Kind {
	case SourceReservoir, SourceRiverIntake, SourceGroundwater:
	default:
		violations = append(violations, FieldViolation{Field: "kind", Rule: "is unsupported"})
	}
	if strings.TrimSpace(s.Timezone) == "" {
		violations = append(violations, FieldViolation{Field: "timezone", Rule: "is required"})
	} else if _, err := time.LoadLocation(s.Timezone); err != nil {
		violations = append(violations, FieldViolation{Field: "timezone", Rule: "must be an IANA timezone"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate water source", violations...)
	}
	return nil
}

type ProtectionZoneLevel string

const (
	ZonePrimary   ProtectionZoneLevel = "primary"
	ZoneSecondary ProtectionZoneLevel = "secondary"
	ZoneBuffer    ProtectionZoneLevel = "buffer"
)

type ProtectionZone struct {
	ID               string
	SourceID         string
	OrganizationID   string
	Name             string
	Level            ProtectionZoneLevel
	AreaSquareMeters int64
	Active           bool
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (z ProtectionZone) Validate() error {
	var violations []FieldViolation
	if z.SourceID == "" {
		violations = append(violations, FieldViolation{Field: "source_id", Rule: "is required"})
	}
	if z.OrganizationID == "" {
		violations = append(violations, FieldViolation{Field: "organization_id", Rule: "is required"})
	}
	if strings.TrimSpace(z.Name) == "" {
		violations = append(violations, FieldViolation{Field: "name", Rule: "is required"})
	}
	switch z.Level {
	case ZonePrimary, ZoneSecondary, ZoneBuffer:
	default:
		violations = append(violations, FieldViolation{Field: "level", Rule: "is unsupported"})
	}
	if z.AreaSquareMeters <= 0 {
		violations = append(violations, FieldViolation{Field: "area_square_meters", Rule: "must be positive"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate protection zone", violations...)
	}
	return nil
}

type MonitoringStation struct {
	ID             string
	SourceID       string
	ZoneID         string
	OrganizationID string
	Code           string
	Name           string
	Latitude       float64
	Longitude      float64
	Active         bool
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (s MonitoringStation) Validate() error {
	var violations []FieldViolation
	if s.SourceID == "" || s.ZoneID == "" || s.OrganizationID == "" {
		violations = append(violations, FieldViolation{Field: "ownership", Rule: "source, zone and organization are required"})
	}
	if strings.TrimSpace(s.Code) == "" {
		violations = append(violations, FieldViolation{Field: "code", Rule: "is required"})
	}
	if strings.TrimSpace(s.Name) == "" {
		violations = append(violations, FieldViolation{Field: "name", Rule: "is required"})
	}
	if s.Latitude < -90 || s.Latitude > 90 {
		violations = append(violations, FieldViolation{Field: "latitude", Rule: "must be between -90 and 90"})
	}
	if s.Longitude < -180 || s.Longitude > 180 {
		violations = append(violations, FieldViolation{Field: "longitude", Rule: "must be between -180 and 180"})
	}
	if len(violations) > 0 {
		return NewValidationError("validate monitoring station", violations...)
	}
	return nil
}
