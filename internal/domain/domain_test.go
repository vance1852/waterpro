package domain

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestRoleCapabilities(t *testing.T) {
	tests := []struct {
		name      string
		role      Role
		valid     bool
		collect   bool
		analyze   bool
		supervise bool
	}{
		{name: "field operator", role: RoleFieldOperator, valid: true, collect: true},
		{name: "lab analyst", role: RoleLabAnalyst, valid: true, analyze: true},
		{name: "supervisor", role: RoleProtectionSupervisor, valid: true, collect: true, analyze: true, supervise: true},
		{name: "unknown", role: Role("guest")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.role.Valid(); got != test.valid {
				t.Fatalf("Valid() = %v, want %v", got, test.valid)
			}
			actor := Actor{UserID: "u1", OrganizationID: "o1", Role: test.role, AuthGeneration: 1}
			if got := actor.CanCollect(); got != test.collect {
				t.Fatalf("CanCollect() = %v, want %v", got, test.collect)
			}
			if got := actor.CanAnalyze(); got != test.analyze {
				t.Fatalf("CanAnalyze() = %v, want %v", got, test.analyze)
			}
			if got := actor.CanSupervise(); got != test.supervise {
				t.Fatalf("CanSupervise() = %v, want %v", got, test.supervise)
			}
		})
	}
}

func TestActorValidation(t *testing.T) {
	valid := Actor{UserID: "u1", OrganizationID: "o1", Role: RoleFieldOperator, AuthGeneration: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid actor rejected: %v", err)
	}
	tests := []Actor{
		{OrganizationID: "o1", Role: RoleFieldOperator, AuthGeneration: 1},
		{UserID: "u1", Role: RoleFieldOperator, AuthGeneration: 1},
		{UserID: "u1", OrganizationID: "o1", Role: "guest", AuthGeneration: 1},
		{UserID: "u1", OrganizationID: "o1", Role: RoleFieldOperator},
	}
	for index, actor := range tests {
		if err := actor.Validate(); !errors.Is(err, ErrValidation) {
			t.Errorf("case %d: error = %v, want validation", index, err)
		}
	}
}

func TestSessionActiveAtRequiresEveryFence(t *testing.T) {
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	user := User{ID: "u1", Active: true, AuthGeneration: 3}
	session := Session{ID: "s1", UserID: "u1", AuthGeneration: 3, ExpiresAt: now.Add(time.Hour)}
	if !session.ActiveAt(now, user) {
		t.Fatal("fresh session should be active")
	}
	revoked := now.Add(-time.Minute)
	session.RevokedAt = &revoked
	if session.ActiveAt(now, user) {
		t.Fatal("revoked session must not be active")
	}
	session.RevokedAt = nil
	session.ExpiresAt = now
	if session.ActiveAt(now, user) {
		t.Fatal("session expiring at now must not be active")
	}
	session.ExpiresAt = now.Add(time.Hour)
	user.Active = false
	if session.ActiveAt(now, user) {
		t.Fatal("inactive user must fence the session")
	}
	user.Active = true
	user.AuthGeneration++
	if session.ActiveAt(now, user) {
		t.Fatal("generation mismatch must fence the session")
	}
}

func TestSourceAndZoneValidation(t *testing.T) {
	valid := WaterSource{OrganizationID: "o1", Name: "North Reservoir", Kind: SourceReservoir, Timezone: "Asia/Shanghai"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}
	invalidSources := []WaterSource{
		{Name: "North Reservoir", Kind: SourceReservoir, Timezone: "UTC"},
		{OrganizationID: "o1", Name: "x", Kind: SourceReservoir, Timezone: "UTC"},
		{OrganizationID: "o1", Name: "North Reservoir", Kind: "lake", Timezone: "UTC"},
		{OrganizationID: "o1", Name: "North Reservoir", Kind: SourceReservoir, Timezone: "Mars/Base"},
	}
	for index, value := range invalidSources {
		if err := value.Validate(); !errors.Is(err, ErrValidation) {
			t.Errorf("source case %d: error = %v", index, err)
		}
	}
	zone := ProtectionZone{SourceID: "src", OrganizationID: "org", Name: "Primary shoreline", Level: ZonePrimary, AreaSquareMeters: 5000}
	if err := zone.Validate(); err != nil {
		t.Fatalf("valid zone rejected: %v", err)
	}
	zone.Level = "restricted"
	if err := zone.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid level error = %v", err)
	}
	zone.Level = ZonePrimary
	zone.AreaSquareMeters = 0
	if err := zone.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid area error = %v", err)
	}
}

func TestMonitoringStationCoordinates(t *testing.T) {
	valid := MonitoringStation{SourceID: "src", ZoneID: "zone", OrganizationID: "org", Code: "N1", Name: "North inlet", Latitude: 31.2, Longitude: 121.4}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid station rejected: %v", err)
	}
	invalid := valid
	invalid.Latitude = 91
	if err := invalid.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("latitude error = %v", err)
	}
	invalid = valid
	invalid.Longitude = -181
	if err := invalid.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("longitude error = %v", err)
	}
}

func TestSamplingPlanTransitions(t *testing.T) {
	allowed := []struct{ from, to SamplingPlanStatus }{{PlanDraft, PlanPublished}, {PlanDraft, PlanCancelled}, {PlanPublished, PlanCompleted}, {PlanPublished, PlanCancelled}}
	for _, transition := range allowed {
		if err := (SamplingPlan{Status: transition.from}).CanTransition(transition.to); err != nil {
			t.Errorf("%s -> %s rejected: %v", transition.from, transition.to, err)
		}
	}
	rejected := []struct{ from, to SamplingPlanStatus }{{PlanDraft, PlanCompleted}, {PlanPublished, PlanDraft}, {PlanCompleted, PlanCancelled}, {PlanCancelled, PlanPublished}}
	for _, transition := range rejected {
		if err := (SamplingPlan{Status: transition.from}).CanTransition(transition.to); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("%s -> %s error = %v", transition.from, transition.to, err)
		}
	}
}

func TestSampleTransitionsAndLabel(t *testing.T) {
	path := []SampleStatus{SamplePlanned, SampleCollected, SampleInTransit, SampleReceived, SampleTested, SampleArchived}
	for index := 0; index < len(path)-1; index++ {
		if err := (Sample{Status: path[index]}).CanTransition(path[index+1]); err != nil {
			t.Fatalf("%s -> %s rejected: %v", path[index], path[index+1], err)
		}
	}
	if err := (Sample{Status: SampleCollected}).CanTransition(SampleArchived); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("skipped custody error = %v", err)
	}
	day := time.Date(2026, 1, 2, 7, 0, 0, 0, time.UTC)
	if got, want := FormatSampleLabel("ab-1", day, 42), "AB-1-20260102-0042"; got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
}

func TestLabResultValidationReviewAndExceedance(t *testing.T) {
	result := LabResult{OrganizationID: "o", SampleID: "s", Parameter: "ammonia", Value: 1.2, Unit: "mg/L", MethodCode: "HJ-535", DetectionLimit: .01, RegulatoryLimit: 1, Status: LabResultSubmitted, AnalystUserID: "analyst", MeasuredAt: time.Now()}
	if err := result.Validate(); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	if !result.ExceedsLimit() {
		t.Fatal("1.2 should exceed a limit of 1")
	}
	if err := result.CanTransition(LabResultApproved, "reviewer"); err != nil {
		t.Fatalf("independent review rejected: %v", err)
	}
	if err := result.CanTransition(LabResultApproved, "analyst"); !errors.Is(err, ErrValidation) {
		t.Fatalf("self-review error = %v", err)
	}
	result.Value = math.NaN()
	if err := result.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("NaN error = %v", err)
	}
}

func TestPermitTransitionsRespectValidity(t *testing.T) {
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	permit := Permit{Status: PermitDraft, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}
	if err := permit.CanTransition(PermitActive, now); err != nil {
		t.Fatalf("valid activation rejected: %v", err)
	}
	permit.ValidFrom = now.Add(time.Hour)
	if err := permit.CanTransition(PermitActive, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("early activation error = %v", err)
	}
}

func TestIncidentAndRemediationTransitionOrder(t *testing.T) {
	incidentPath := []IncidentStatus{IncidentReported, IncidentAssessing, IncidentContained, IncidentRemediating, IncidentResolved}
	for index := 0; index < len(incidentPath)-1; index++ {
		if err := (Incident{Status: incidentPath[index]}).CanTransition(incidentPath[index+1]); err != nil {
			t.Fatalf("%s -> %s rejected: %v", incidentPath[index], incidentPath[index+1], err)
		}
	}
	if err := (Incident{Status: IncidentReported}).CanTransition(IncidentResolved); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("skipped phases error = %v", err)
	}
	remediationPath := []RemediationStatus{RemediationDraft, RemediationApproved, RemediationExecuting, RemediationVerified, RemediationClosed}
	for index := 0; index < len(remediationPath)-1; index++ {
		if err := (RemediationPlan{Status: remediationPath[index]}).CanTransition(remediationPath[index+1]); err != nil {
			t.Fatalf("%s -> %s rejected: %v", remediationPath[index], remediationPath[index+1], err)
		}
	}
}

func TestTelemetryValidationRetryAndPagination(t *testing.T) {
	now := time.Now().UTC()
	reading := TelemetryReading{OrganizationID: "o", StationID: "s", ExternalID: "e", Parameter: "pH", Value: 6.8, Unit: "pH", Threshold: 8, ObservedAt: now}
	if err := reading.Validate(now); err != nil {
		t.Fatalf("valid reading rejected: %v", err)
	}
	reading.ObservedAt = now.Add(6 * time.Minute)
	if err := reading.Validate(now); !errors.Is(err, ErrValidation) {
		t.Fatalf("future reading error = %v", err)
	}
	if got := RetryDelay(1); got != time.Second {
		t.Fatalf("retry 1 = %s", got)
	}
	if got := RetryDelay(4); got != 8*time.Second {
		t.Fatalf("retry 4 = %s", got)
	}
	if got := RetryDelay(100); got != 128*time.Second {
		t.Fatalf("capped retry = %s", got)
	}
	if got := (PageRequest{}).Normalized().Limit; got != 50 {
		t.Fatalf("default limit = %d", got)
	}
	if got := (PageRequest{Limit: 500}).Normalized().Limit; got != 200 {
		t.Fatalf("capped limit = %d", got)
	}
}

func TestTypedErrorsPreserveClassification(t *testing.T) {
	checks := []struct {
		err    error
		target error
	}{
		{&ValidationError{Operation: "test"}, ErrValidation},
		{&NotFoundError{Resource: "sample", ID: "s1"}, ErrNotFound},
		{&ConflictError{Resource: "sample", Key: "s1", Cause: errors.New("version")}, ErrConflict},
		{&TransitionError{Entity: "sample", From: "a", To: "b", Reason: "no"}, ErrInvalidTransition},
		{&LeaseError{Resource: "job", Owner: "w1", Generation: 2}, ErrLeaseLost},
	}
	for _, check := range checks {
		if !errors.Is(check.err, check.target) {
			t.Errorf("%T does not unwrap to %v", check.err, check.target)
		}
	}
}
