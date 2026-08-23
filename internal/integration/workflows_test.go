package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/auth"
	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/incident"
	"github.com/vance1852/waterpro/internal/laboratory"
	"github.com/vance1852/waterpro/internal/permit"
	"github.com/vance1852/waterpro/internal/remediation"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
	"github.com/vance1852/waterpro/internal/sampling"
	"github.com/vance1852/waterpro/internal/source"
	"github.com/vance1852/waterpro/internal/telemetry"
)

type fixture struct {
	store       *repository.Store
	auth        *auth.Service
	sources     *source.Service
	sampling    *sampling.Service
	lab         *laboratory.Service
	permits     *permit.Service
	incidents   *incident.Service
	remediation *remediation.Service
	telemetry   *telemetry.Service
	supervisor  domain.Actor
	field       domain.Actor
	analyst     domain.Actor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "integration.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	now := time.Now().UTC()
	if err := store.CreateOrganization(context.Background(), "org-1", "Watershed Authority", now); err != nil {
		t.Fatal(err)
	}
	passwordHash, err := auth.HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	users := []domain.User{
		{ID: "supervisor", OrganizationID: "org-1", Email: "supervisor@example.test", PasswordHash: passwordHash, Role: domain.RoleProtectionSupervisor, Active: true, AuthGeneration: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "field", OrganizationID: "org-1", Email: "field@example.test", PasswordHash: passwordHash, Role: domain.RoleFieldOperator, Active: true, AuthGeneration: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "analyst", OrganizationID: "org-1", Email: "analyst@example.test", PasswordHash: passwordHash, Role: domain.RoleLabAnalyst, Active: true, AuthGeneration: 1, CreatedAt: now, UpdatedAt: now},
	}
	for _, user := range users {
		if err := store.CreateUser(context.Background(), user); err != nil {
			t.Fatalf("create user %s: %v", user.ID, err)
		}
	}
	return &fixture{
		store:       store,
		auth:        auth.NewService(store, time.Hour),
		sources:     source.NewService(store),
		sampling:    sampling.NewService(store),
		lab:         laboratory.NewService(store),
		permits:     permit.NewService(store),
		incidents:   incident.NewService(store, 5*time.Minute),
		remediation: remediation.NewService(store),
		telemetry:   telemetry.NewService(store),
		supervisor:  domain.Actor{UserID: "supervisor", OrganizationID: "org-1", Role: domain.RoleProtectionSupervisor, AuthGeneration: 1},
		field:       domain.Actor{UserID: "field", OrganizationID: "org-1", Role: domain.RoleFieldOperator, AuthGeneration: 1},
		analyst:     domain.Actor{UserID: "analyst", OrganizationID: "org-1", Role: domain.RoleLabAnalyst, AuthGeneration: 1},
	}
}

type sourceGraph struct {
	source  domain.WaterSource
	zone    domain.ProtectionZone
	station domain.MonitoringStation
}

func (f *fixture) createSourceGraph(t *testing.T) sourceGraph {
	t.Helper()
	ctx := context.Background()
	createdSource, err := f.sources.RegisterWaterSource(ctx, f.supervisor, source.RegisterSourceCommand{Name: "North Reservoir", Kind: domain.SourceReservoir, Timezone: "UTC", RequestID: "request-source"})
	if err != nil {
		t.Fatalf("register source: %v", err)
	}
	zone, err := f.sources.RegisterZone(ctx, f.supervisor, source.RegisterZoneCommand{SourceID: createdSource.ID, Name: "Primary shoreline", Level: domain.ZonePrimary, AreaSquareMeters: 50000, RequestID: "request-zone"})
	if err != nil {
		t.Fatalf("register zone: %v", err)
	}
	station, err := f.sources.RegisterStation(ctx, f.supervisor, source.RegisterStationCommand{SourceID: createdSource.ID, ZoneID: zone.ID, Code: "NORTH-1", Name: "North inlet", Latitude: 31.2, Longitude: 121.4, RequestID: "request-station"})
	if err != nil {
		t.Fatalf("register station: %v", err)
	}
	return sourceGraph{source: createdSource, zone: zone, station: station}
}

func (f *fixture) createReceivedSample(t *testing.T, graph sourceGraph) domain.Sample {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	plan, err := f.sampling.CreatePlan(ctx, f.supervisor, sampling.CreatePlanCommand{
		SourceID:        graph.source.ID,
		StationID:       graph.station.ID,
		AssignedUserID:  f.field.UserID,
		WindowStart:     now.Add(-time.Hour),
		WindowEnd:       now.Add(time.Hour),
		RequiredBottles: 2,
		RequestID:       "request-plan-received",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if err := f.sampling.PublishPlan(ctx, f.supervisor, plan.ID, "request-publish-received"); err != nil {
		t.Fatalf("publish plan: %v", err)
	}
	sample, err := f.sampling.Collect(ctx, f.field, sampling.CollectCommand{PlanID: plan.ID, BottleCount: 2, CollectedAt: now, RequestID: "request-collect-received"})
	if err != nil {
		t.Fatalf("collect sample: %v", err)
	}
	sample, err = f.sampling.Handoff(ctx, f.field, sampling.HandoffCommand{SampleID: sample.ID, ToUserID: f.supervisor.UserID, OccurredAt: now.Add(time.Minute), RequestID: "request-transit-received"})
	if err != nil {
		t.Fatalf("handoff to transit: %v", err)
	}
	sample, err = f.sampling.Handoff(ctx, f.supervisor, sampling.HandoffCommand{SampleID: sample.ID, ToUserID: f.analyst.UserID, OccurredAt: now.Add(2 * time.Minute), RequestID: "request-lab-received"})
	if err != nil {
		t.Fatalf("handoff to lab: %v", err)
	}
	return sample
}

func TestAuthenticationLifecyclePersistsRevocationAndGeneration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	login, err := f.auth.Login(ctx, auth.LoginCommand{OrganizationID: "org-1", Email: "field@example.test", Password: "correct-horse-battery-staple"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if login.Token == "" || !login.ExpiresAt.After(time.Now()) {
		t.Fatalf("invalid login result: %#v", login)
	}
	actor, err := f.auth.Authenticate(ctx, login.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if actor.UserID != "field" || actor.Role != domain.RoleFieldOperator {
		t.Fatalf("actor = %#v", actor)
	}
	if err := f.auth.Logout(ctx, login.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := f.auth.Authenticate(ctx, login.Token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked auth error = %v", err)
	}
	second, err := f.auth.Login(ctx, auth.LoginCommand{OrganizationID: "org-1", Email: "field@example.test", Password: "correct-horse-battery-staple"})
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if err := f.auth.SetUserActive(ctx, f.supervisor, "field", false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := f.auth.Authenticate(ctx, second.Token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("generation fenced auth error = %v", err)
	}
	if _, err := f.auth.Login(ctx, auth.LoginCommand{OrganizationID: "org-1", Email: "field@example.test", Password: "correct-horse-battery-staple"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("inactive login error = %v", err)
	}
}

func TestAuthenticationRejectsBadPasswordAndPersistsFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, err := f.auth.Login(ctx, auth.LoginCommand{OrganizationID: "org-1", Email: "field@example.test", Password: "incorrect-password"})
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("bad password error = %v", err)
	}
	var failures int
	if err := f.store.DB().QueryRowContext(ctx, `SELECT failed_login_count FROM users WHERE id = 'field'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Fatalf("failed_login_count = %d", failures)
	}
}

func TestSourceGraphRegistrationIsTenantConsistentAndAudited(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	if graph.zone.SourceID != graph.source.ID {
		t.Fatalf("zone source = %s", graph.zone.SourceID)
	}
	if graph.station.SourceID != graph.source.ID || graph.station.ZoneID != graph.zone.ID {
		t.Fatalf("station ownership = %#v", graph.station)
	}
	var audits int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE request_id IN ('request-source','request-zone','request-station')`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("audit count = %d", audits)
	}
	sources, err := f.sources.ListSources(context.Background(), f.field, true, domain.PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources.Items) != 1 || sources.Items[0].ID != graph.source.ID {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestStationCannotCrossProtectionZoneSourceBoundary(t *testing.T) {
	f := newFixture(t)
	first := f.createSourceGraph(t)
	second, err := f.sources.RegisterWaterSource(context.Background(), f.supervisor, source.RegisterSourceCommand{Name: "South Wellfield", Kind: domain.SourceGroundwater, Timezone: "UTC", RequestID: "request-source-2"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.sources.RegisterStation(context.Background(), f.supervisor, source.RegisterStationCommand{SourceID: second.ID, ZoneID: first.zone.ID, Code: "CROSS", Name: "Cross source station", Latitude: 30, Longitude: 120, RequestID: "request-cross"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cross-source station error = %v", err)
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM monitoring_stations WHERE code = 'CROSS'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cross-source station count = %d", count)
	}
}

func TestSamplingWorkflowMaintainsCustodyAndCompletesPlan(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	sample := f.createReceivedSample(t, graph)
	if sample.Status != domain.SampleReceived {
		t.Fatalf("sample status = %s", sample.Status)
	}
	if sample.CustodianUserID != f.analyst.UserID {
		t.Fatalf("custodian = %s", sample.CustodianUserID)
	}
	history, err := f.sampling.CustodyHistory(context.Background(), f.supervisor, sample.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("custody event count = %d", len(history))
	}
	wantActions := []string{"collected", "in_transit", "received"}
	for index, action := range wantActions {
		if history[index].Action != action {
			t.Errorf("history[%d].Action = %q", index, history[index].Action)
		}
	}
	var status string
	if err := f.store.DB().QueryRow(`SELECT sp.status FROM sampling_plans sp JOIN samples s ON s.plan_id = sp.id WHERE s.id = ?`, sample.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("plan status = %s", status)
	}
}

func TestSamplingRejectsWrongBottleCountWithoutConsumingSequence(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	now := time.Now().UTC()
	plan, err := f.sampling.CreatePlan(context.Background(), f.supervisor, sampling.CreatePlanCommand{SourceID: graph.source.ID, StationID: graph.station.ID, AssignedUserID: f.field.UserID, WindowStart: now.Add(-time.Hour), WindowEnd: now.Add(time.Hour), RequiredBottles: 3, RequestID: "plan-bottles"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.sampling.PublishPlan(context.Background(), f.supervisor, plan.ID, "publish-bottles"); err != nil {
		t.Fatal(err)
	}
	_, err = f.sampling.Collect(context.Background(), f.field, sampling.CollectCommand{PlanID: plan.ID, BottleCount: 2, CollectedAt: now, RequestID: "collect-bottles"})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("wrong bottle error = %v", err)
	}
	var sequences, samples int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM sample_sequences`).Scan(&sequences); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if sequences != 0 || samples != 0 {
		t.Fatalf("failed collection leaked sequences=%d samples=%d", sequences, samples)
	}
}

func TestLaboratoryApprovalCreatesAtomicExceedanceIncident(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	sample := f.createReceivedSample(t, graph)
	now := time.Now().UTC()
	result, err := f.lab.RecordResult(context.Background(), f.analyst, laboratory.RecordResultCommand{SampleID: sample.ID, Parameter: "ammonia", Value: 2.5, Unit: "mg/L", MethodCode: "HJ-535", DetectionLimit: .01, RegulatoryLimit: 1, MeasuredAt: now, RequestID: "lab-record"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.lab.Submit(context.Background(), f.analyst, result.ID, "lab-submit"); err != nil {
		t.Fatal(err)
	}
	incidentID, err := f.lab.Review(context.Background(), f.supervisor, result.ID, true, "lab-review")
	if err != nil {
		t.Fatal(err)
	}
	if incidentID == "" {
		t.Fatal("approval did not create exceedance incident")
	}
	var incidentCount, outboxCount int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM incidents WHERE id = ? AND originating_result_id = ?`, incidentID, result.ID).Scan(&incidentCount); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, incidentID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if incidentCount != 1 || outboxCount != 1 {
		t.Fatalf("incident=%d outbox=%d", incidentCount, outboxCount)
	}
	var sampleStatus string
	if err := f.store.DB().QueryRow(`SELECT status FROM samples WHERE id = ?`, sample.ID).Scan(&sampleStatus); err != nil {
		t.Fatal(err)
	}
	if sampleStatus != "tested" {
		t.Fatalf("sample status = %s", sampleStatus)
	}
}

func TestLaboratorySelfReviewRollsBackAllState(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	sample := f.createReceivedSample(t, graph)
	result, err := f.lab.RecordResult(context.Background(), f.analyst, laboratory.RecordResultCommand{SampleID: sample.ID, Parameter: "nitrate", Value: 20, Unit: "mg/L", MethodCode: "HJ-84", DetectionLimit: .1, RegulatoryLimit: 10, MeasuredAt: time.Now(), RequestID: "lab-record-self"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.lab.Submit(context.Background(), f.analyst, result.ID, "lab-submit-self"); err != nil {
		t.Fatal(err)
	}
	_, err = f.lab.Review(context.Background(), f.analyst, result.ID, true, "lab-review-self")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("self review error = %v", err)
	}
	var status string
	if err := f.store.DB().QueryRow(`SELECT status FROM lab_results WHERE id = ?`, result.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "submitted" {
		t.Fatalf("result status after failed review = %s", status)
	}
	var incidents int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM incidents WHERE originating_result_id = ?`, result.ID).Scan(&incidents); err != nil {
		t.Fatal(err)
	}
	if incidents != 0 {
		t.Fatalf("failed review created %d incidents", incidents)
	}
}

func TestPermitActivationAndDischargeIdempotency(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	now := time.Now().UTC()
	created, err := f.permits.Create(context.Background(), f.supervisor, permit.CreateCommand{SourceID: graph.source.ID, HolderName: "Municipal Treatment Plant", Reference: "PERMIT-2026-1", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(24 * time.Hour), DailyVolumeLimitLiters: 1000, RequestID: "permit-create"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.permits.Activate(context.Background(), f.supervisor, created.ID, "permit-activate"); err != nil {
		t.Fatal(err)
	}
	command := permit.ReportDischargeCommand{PermitID: created.ID, IdempotencyKey: "discharge-key", VolumeLiters: 400, OccurredAt: now, RequestID: "discharge-1"}
	first, inserted, err := f.permits.ReportDischarge(context.Background(), f.field, command)
	if err != nil || !inserted {
		t.Fatalf("first discharge inserted=%v err=%v", inserted, err)
	}
	second, inserted, err := f.permits.ReportDischarge(context.Background(), f.field, command)
	if err != nil || inserted {
		t.Fatalf("repeat discharge inserted=%v err=%v", inserted, err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent result ids differ: %s %s", first.ID, second.ID)
	}
	command.VolumeLiters = 401
	if _, _, err := f.permits.ReportDischarge(context.Background(), f.field, command); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("key reuse error = %v", err)
	}
	command.IdempotencyKey = "discharge-over-limit"
	command.VolumeLiters = 700
	if _, _, err := f.permits.ReportDischarge(context.Background(), f.field, command); !errors.Is(err, domain.ErrCapacityExceeded) {
		t.Fatalf("capacity error = %v", err)
	}
}

func TestPermitActivationBlockedByOpenExceedance(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	now := time.Now().UTC()
	sample := f.createReceivedSample(t, graph)
	result, err := f.lab.RecordResult(context.Background(), f.analyst, laboratory.RecordResultCommand{SampleID: sample.ID, Parameter: "mercury", Value: .02, Unit: "mg/L", MethodCode: "HJ-694", DetectionLimit: .0001, RegulatoryLimit: .001, MeasuredAt: now, RequestID: "permit-block-result"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.lab.Submit(context.Background(), f.analyst, result.ID, "permit-block-submit"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.lab.Review(context.Background(), f.supervisor, result.ID, true, "permit-block-review"); err != nil {
		t.Fatal(err)
	}
	created, err := f.permits.Create(context.Background(), f.supervisor, permit.CreateCommand{SourceID: graph.source.ID, HolderName: "Treatment Plant", Reference: "BLOCKED-1", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), DailyVolumeLimitLiters: 100, RequestID: "permit-block-create"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.permits.Activate(context.Background(), f.supervisor, created.ID, "permit-block-active"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("blocked activation error = %v", err)
	}
}

func TestIncidentClaimAndLeaseFenceAssignments(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	reported, err := f.incidents.Report(context.Background(), f.field, incident.ReportCommand{SourceID: graph.source.ID, Title: "Fuel spill near intake", Description: "A fuel sheen was observed upstream of the intake", Severity: domain.SeverityCritical, RequestID: "incident-report"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.incidents.Claim(context.Background(), f.supervisor, reported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.LeaseToken == "" || claimed.LeaseGeneration != 1 {
		t.Fatalf("claimed incident = %#v", claimed)
	}
	_, err = f.incidents.AssignContainment(context.Background(), f.supervisor, incident.AssignCommand{IncidentID: reported.ID, LeaseToken: "stale", ResourceCode: "BOOM-1", AssigneeUserID: f.field.UserID, RequestID: "assign-stale"})
	if !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("stale lease error = %v", err)
	}
	assignment, err := f.incidents.AssignContainment(context.Background(), f.supervisor, incident.AssignCommand{IncidentID: reported.ID, LeaseToken: claimed.LeaseToken, ResourceCode: "BOOM-1", AssigneeUserID: f.field.UserID, RequestID: "assign-valid"})
	if err != nil {
		t.Fatal(err)
	}
	if assignment.Status != domain.AssignmentPending {
		t.Fatalf("assignment status = %s", assignment.Status)
	}
	if err := f.incidents.Advance(context.Background(), f.supervisor, reported.ID, claimed.LeaseToken, domain.IncidentAssessing, "advance-assessing"); err != nil {
		t.Fatal(err)
	}
}

func TestIncidentCannotResolveWithIncompleteContainment(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	reported, err := f.incidents.Report(context.Background(), f.field, incident.ReportCommand{SourceID: graph.source.ID, Title: "Chemical runoff alert", Description: "Runoff entered a tributary after a containment breach", Severity: domain.SeveritySignificant, RequestID: "incident-runoff"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.incidents.Claim(context.Background(), f.supervisor, reported.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.incidents.AssignContainment(context.Background(), f.supervisor, incident.AssignCommand{IncidentID: reported.ID, LeaseToken: claimed.LeaseToken, ResourceCode: "BARRIER-2", AssigneeUserID: f.field.UserID, RequestID: "assign-runoff"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE incidents SET status = 'remediating' WHERE id = ?`, reported.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.incidents.Advance(context.Background(), f.supervisor, reported.ID, claimed.LeaseToken, domain.IncidentResolved, "resolve-runoff"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("resolve error = %v", err)
	}
}

func TestRemediationPlanApprovalAndActionCompletion(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	reported, err := f.incidents.Report(context.Background(), f.field, incident.ReportCommand{SourceID: graph.source.ID, Title: "Sediment plume response", Description: "A sediment plume requires staged shoreline remediation", Severity: domain.SeveritySignificant, RequestID: "incident-sediment"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.remediation.CreatePlan(context.Background(), f.supervisor, remediation.CreatePlanCommand{IncidentID: reported.ID, Title: "Shoreline recovery", Objective: "Remove contaminated sediment and verify water quality", BudgetCents: 500000, Actions: []remediation.CreateAction{{IdempotencyKey: "remove-sediment", Description: "Remove impacted sediment"}, {IdempotencyKey: "verify-water", Description: "Verify downstream water quality"}}, RequestID: "remediation-create"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.remediation.Approve(context.Background(), f.supervisor, plan.ID, "remediation-approve"); err != nil {
		t.Fatal(err)
	}
	rows, err := f.store.DB().Query(`SELECT id, version FROM remediation_actions WHERE plan_id = ? ORDER BY id`, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actionIDs []string
	for rows.Next() {
		var id string
		var version int64
		if err := rows.Scan(&id, &version); err != nil {
			t.Fatal(err)
		}
		actionIDs = append(actionIDs, id)
	}
	if len(actionIDs) != 2 {
		t.Fatalf("action count = %d", len(actionIDs))
	}
	if err := f.remediation.CompleteAction(context.Background(), f.field, remediation.CompleteActionCommand{PlanID: plan.ID, ActionID: actionIDs[0], ExpectedVersion: 1, Success: true, RequestID: "action-complete"}); err != nil {
		t.Fatal(err)
	}
	if err := f.remediation.CompleteAction(context.Background(), f.field, remediation.CompleteActionCommand{PlanID: plan.ID, ActionID: actionIDs[0], ExpectedVersion: 1, Success: true, RequestID: "action-repeat"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale action error = %v", err)
	}
}

func TestRemediationCreationRollsBackDuplicateActionKeys(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	reported, err := f.incidents.Report(context.Background(), f.field, incident.ReportCommand{SourceID: graph.source.ID, Title: "Bank erosion response", Description: "Erosion is carrying sediment into the protected source", Severity: domain.SeverityAdvisory, RequestID: "incident-erosion"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.remediation.CreatePlan(context.Background(), f.supervisor, remediation.CreatePlanCommand{IncidentID: reported.ID, Title: "Bank stabilization", Objective: "Stabilize the bank before the next rainfall event", BudgetCents: 100000, Actions: []remediation.CreateAction{{IdempotencyKey: "same", Description: "Install erosion mat"}, {IdempotencyKey: "same", Description: "Plant bank cover"}}, RequestID: "remediation-duplicate"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate key error = %v", err)
	}
	var plans int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM remediation_plans WHERE incident_id = ?`, reported.ID).Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if plans != 0 {
		t.Fatalf("failed creation leaked %d plans", plans)
	}
}

func TestTelemetryIngestIsIdempotentAndCreatesSingleAlertJob(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	command := telemetry.IngestCommand{StationID: graph.station.ID, ExternalID: "sensor-reading-1", Parameter: "turbidity", Value: 15, Unit: "NTU", Threshold: 5, ObservedAt: time.Now().UTC(), RequestID: "telemetry-1"}
	first, created, err := f.telemetry.Ingest(context.Background(), f.field, command)
	if err != nil || !created {
		t.Fatalf("first ingest created=%v err=%v", created, err)
	}
	_, created, err = f.telemetry.Ingest(context.Background(), f.field, command)
	if err != nil || created {
		t.Fatalf("repeat ingest created=%v err=%v", created, err)
	}
	var readings, jobs, audits int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM telemetry_readings WHERE station_id = ?`, graph.station.ID).Scan(&readings); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM alert_jobs WHERE reading_id = ?`, first.ID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'telemetry.ingest'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if readings != 1 || jobs != 1 || audits != 1 {
		t.Fatalf("readings=%d jobs=%d audits=%d", readings, jobs, audits)
	}
}

func TestConcurrentSampleSequenceAllocationsAreUnique(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	ctx := context.Background()
	const workers = 8
	results := make(chan int64, workers)
	errorsCh := make(chan error, workers)
	start := make(chan struct{})
	for index := 0; index < workers; index++ {
		go func() {
			<-start
			var value int64
			err := f.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
				var err error
				value, err = f.store.NextSampleSequence(ctx, tx, "org-1", graph.station.ID, "2026-08-23")
				return err
			})
			if err != nil {
				errorsCh <- err
				return
			}
			results <- value
		}()
	}
	close(start)
	seen := map[int64]bool{}
	for count := 0; count < workers; count++ {
		select {
		case err := <-errorsCh:
			t.Fatalf("allocation error: %v", err)
		case value := <-results:
			if seen[value] {
				t.Fatalf("duplicate sequence %d", value)
			}
			seen[value] = true
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent allocation timed out")
		}
	}
	if len(seen) != workers {
		t.Fatalf("sequence count = %d", len(seen))
	}
}

func TestAuditFailureRollsBackSourceRegistration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.store.DB().ExecContext(ctx, `DROP TABLE audit_events`); err != nil {
		t.Fatal(err)
	}
	_, err := f.sources.RegisterWaterSource(ctx, f.supervisor, source.RegisterSourceCommand{Name: "Rollback Reservoir", Kind: domain.SourceReservoir, Timezone: "UTC", RequestID: "source-rollback"})
	if err == nil {
		t.Fatal("registration unexpectedly succeeded without audit table")
	}
	var count int
	if err := f.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM water_sources WHERE name = 'Rollback Reservoir'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("audit failure leaked %d sources", count)
	}
}

func TestContextCancellationPreventsTransactionCommit(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := f.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO organizations(id, name, created_at) VALUES ('cancelled-org', 'Cancelled', ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled transaction error = %v", err)
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM organizations WHERE id = 'cancelled-org'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cancelled transaction committed %d rows", count)
	}
}

func TestTenantQueriesDoNotExposeOtherOrganization(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := f.store.DB().Exec(`INSERT INTO organizations(id, name, created_at) VALUES ('org-2', 'Other Authority', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`INSERT INTO water_sources(id, organization_id, name, kind, timezone, active, version, created_at, updated_at) VALUES ('source-other', 'org-2', 'Other Source', 'reservoir', 'UTC', 1, 1, ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	page, err := f.sources.ListSources(context.Background(), f.field, true, domain.PageRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != graph.source.ID {
		t.Fatalf("tenant source page = %#v", page.Items)
	}
}

func TestDatabaseQueriesReturnWrappedNotFoundErrors(t *testing.T) {
	f := newFixture(t)
	checks := []func() error{
		func() error {
			_, err := f.store.WaterSource(context.Background(), f.store.DB(), "org-1", "missing")
			return err
		},
		func() error {
			_, err := f.store.Sample(context.Background(), f.store.DB(), "org-1", "missing")
			return err
		},
		func() error {
			_, err := f.store.Permit(context.Background(), f.store.DB(), "org-1", "missing")
			return err
		},
		func() error {
			_, err := f.store.Incident(context.Background(), f.store.DB(), "org-1", "missing")
			return err
		},
		func() error {
			_, err := f.store.RemediationPlan(context.Background(), f.store.DB(), "org-1", "missing")
			return err
		},
	}
	for index, check := range checks {
		if err := check(); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("check %d error = %v", index, err)
		}
	}
}

func Example_endToEndProtectionFlow() {
	fmt.Println("register source -> schedule sample -> preserve custody -> approve result -> respond to incident")
	// Output: register source -> schedule sample -> preserve custody -> approve result -> respond to incident
}
