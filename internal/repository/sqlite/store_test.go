package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "waterpro-test.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func seedOwnershipGraph(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	if err := store.CreateOrganization(ctx, "org-1", "River Authority", now); err != nil {
		t.Fatalf("CreateOrganization() error = %v", err)
	}
	users := []domain.User{
		{ID: "supervisor", OrganizationID: "org-1", Email: "supervisor@example.test", PasswordHash: "hash", Role: domain.RoleProtectionSupervisor, Active: true, AuthGeneration: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "field", OrganizationID: "org-1", Email: "field@example.test", PasswordHash: "hash", Role: domain.RoleFieldOperator, Active: true, AuthGeneration: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "analyst", OrganizationID: "org-1", Email: "analyst@example.test", PasswordHash: "hash", Role: domain.RoleLabAnalyst, Active: true, AuthGeneration: 1, CreatedAt: now, UpdatedAt: now},
	}
	for _, user := range users {
		if err := store.CreateUser(ctx, user); err != nil {
			t.Fatalf("CreateUser(%s) error = %v", user.ID, err)
		}
	}
	source := domain.WaterSource{ID: "source-1", OrganizationID: "org-1", Name: "North Reservoir", Kind: domain.SourceReservoir, Timezone: "UTC", Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := InsertWaterSource(ctx, store.DB(), source); err != nil {
		t.Fatalf("InsertWaterSource() error = %v", err)
	}
	zone := domain.ProtectionZone{ID: "zone-1", SourceID: source.ID, OrganizationID: "org-1", Name: "Primary zone", Level: domain.ZonePrimary, AreaSquareMeters: 1000, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := InsertProtectionZone(ctx, store.DB(), zone); err != nil {
		t.Fatalf("InsertProtectionZone() error = %v", err)
	}
	station := domain.MonitoringStation{ID: "station-1", SourceID: source.ID, ZoneID: zone.ID, OrganizationID: "org-1", Code: "NORTH", Name: "North intake", Latitude: 31, Longitude: 121, Active: true, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := InsertMonitoringStation(ctx, store.DB(), station); err != nil {
		t.Fatalf("InsertMonitoringStation() error = %v", err)
	}
}

func TestMigrateBuildsExpectedRelationalSchema(t *testing.T) {
	store := openTestStore(t)
	rows, err := store.DB().QueryContext(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		seen[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	required := []string{"organizations", "users", "sessions", "water_sources", "protection_zones", "monitoring_stations", "sampling_plans", "samples", "custody_events", "lab_results", "incidents", "containment_assignments", "remediation_plans", "remediation_actions", "permits", "discharge_events", "telemetry_readings", "alert_jobs", "audit_events", "outbox_events", "idempotency_records"}
	for _, table := range required {
		if !seen[table] {
			t.Errorf("migration did not create %s", table)
		}
	}
	var versions int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&versions); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if versions != 1 {
		t.Fatalf("migration count = %d, want 1", versions)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&versions); err != nil {
		t.Fatalf("count migrations after repeat: %v", err)
	}
	if versions != 1 {
		t.Fatalf("migration count after repeat = %d", versions)
	}
}

func TestForeignKeysRejectCrossEntityOrphans(t *testing.T) {
	store := openTestStore(t)
	_, err := store.DB().Exec(`INSERT INTO monitoring_stations(id, source_id, zone_id, organization_id, code, name, latitude, longitude, active, version, created_at, updated_at) VALUES ('station', 'missing', 'missing', 'missing', 'S', 'orphan', 0, 0, 1, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("orphan station insert unexpectedly succeeded")
	}
}

func TestWithTxCommitsAndRollsBack(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO organizations(id, name, created_at) VALUES ('commit-org', 'Committed', '2026-01-01T00:00:00Z')`)
		return err
	}); err != nil {
		t.Fatalf("committing transaction error = %v", err)
	}
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM organizations WHERE id = 'commit-org'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("committed rows = %d", count)
	}
	sentinel := errors.New("audit dependency failed")
	err := store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO organizations(id, name, created_at) VALUES ('rollback-org', 'Rolled Back', '2026-01-01T00:00:00Z')`); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("rollback error = %v, want sentinel", err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM organizations WHERE id = 'rollback-org'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled back rows = %d", count)
	}
}

func TestDatabaseStateSurvivesCloseAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	ctx := context.Background()
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CreateOrganization(ctx, "persisted", "Persistent Authority", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var name string
	if err := second.DB().QueryRowContext(ctx, `SELECT name FROM organizations WHERE id = 'persisted'`).Scan(&name); err != nil {
		t.Fatalf("reopened query: %v", err)
	}
	if name != "Persistent Authority" {
		t.Fatalf("reopened name = %q", name)
	}
}

func TestSessionPrincipalUsesRevocationExpiryAndGeneration(t *testing.T) {
	store := openTestStore(t)
	seedOwnershipGraph(t, store)
	ctx := context.Background()
	now := time.Now().UTC()
	session := domain.Session{ID: "session-1", UserID: "field", TokenHash: "token-hash", AuthGeneration: 1, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := store.CompleteLogin(ctx, "field", "", session, now); err != nil {
		t.Fatalf("CompleteLogin() error = %v", err)
	}
	actor, err := store.SessionPrincipal(ctx, "token-hash", now)
	if err != nil {
		t.Fatalf("SessionPrincipal() error = %v", err)
	}
	if actor.UserID != "field" || actor.OrganizationID != "org-1" || actor.Role != domain.RoleFieldOperator {
		t.Fatalf("principal = %#v", actor)
	}
	if err := store.SetUserActive(ctx, "org-1", "field", false, now.Add(time.Minute)); err != nil {
		t.Fatalf("SetUserActive() error = %v", err)
	}
	if _, err := store.SessionPrincipal(ctx, "token-hash", now.Add(2*time.Minute)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("stale principal error = %v", err)
	}
}

func TestSampleSequenceAdvancesInsideTransaction(t *testing.T) {
	store := openTestStore(t)
	seedOwnershipGraph(t, store)
	ctx := context.Background()
	var first, second int64
	if err := store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var err error
		first, err = store.NextSampleSequence(ctx, tx, "org-1", "station-1", "2026-08-23")
		if err != nil {
			return err
		}
		second, err = store.NextSampleSequence(ctx, tx, "org-1", "station-1", "2026-08-23")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 {
		t.Fatalf("sequences = %d, %d", first, second)
	}
	err := store.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := store.NextSampleSequence(ctx, tx, "org-1", "station-1", "2026-08-23"); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("rollback transaction unexpectedly succeeded")
	}
	var next int64
	if err := store.DB().QueryRow(`SELECT next_value FROM sample_sequences WHERE organization_id = 'org-1' AND station_id = 'station-1' AND business_day = '2026-08-23'`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != 3 {
		t.Fatalf("next value after rollback = %d, want 3", next)
	}
}

func TestOptimisticSourceUpdateRejectsStaleVersion(t *testing.T) {
	store := openTestStore(t)
	seedOwnershipGraph(t, store)
	now := time.Now().UTC()
	if err := store.UpdateSourceActive(context.Background(), "org-1", "source-1", 1, false, now); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := store.UpdateSourceActive(context.Background(), "org-1", "source-1", 1, true, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
}

func TestOutboxClaimCompletionUsesLeaseFencing(t *testing.T) {
	store := openTestStore(t)
	seedOwnershipGraph(t, store)
	ctx := context.Background()
	now := time.Now().UTC()
	event := domain.OutboxEvent{ID: "outbox-1", OrganizationID: "org-1", Topic: "incident.reported", AggregateType: "incident", AggregateID: "i1", IdempotencyKey: "key-1", Payload: []byte(`{"incident":"i1"}`), Status: domain.OutboxPending, MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	if err := InsertOutboxEvent(ctx, store.DB(), event); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOutboxEvent(ctx, "worker-a", "token-a", now, time.Minute)
	if err != nil {
		t.Fatalf("ClaimOutboxEvent() error = %v", err)
	}
	stale := claimed
	stale.LeaseToken = "wrong-token"
	if err := store.CompleteOutboxEvent(ctx, stale, now.Add(time.Second)); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("stale completion error = %v", err)
	}
	if err := store.CompleteOutboxEvent(ctx, claimed, now.Add(time.Second)); err != nil {
		t.Fatalf("valid completion error = %v", err)
	}
	var status string
	if err := store.DB().QueryRow(`SELECT status FROM outbox_events WHERE id = 'outbox-1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" {
		t.Fatalf("status = %q", status)
	}
}

func TestAlertJobFailureRetriesThenDies(t *testing.T) {
	store := openTestStore(t)
	seedOwnershipGraph(t, store)
	ctx := context.Background()
	now := time.Now().UTC()
	reading := domain.TelemetryReading{ID: "reading-1", OrganizationID: "org-1", StationID: "station-1", ExternalID: "external-1", Parameter: "turbidity", Value: 12, Unit: "NTU", Threshold: 5, ObservedAt: now, ReceivedAt: now}
	if created, err := InsertTelemetryReading(ctx, store.DB(), reading); err != nil || !created {
		t.Fatalf("reading created=%v err=%v", created, err)
	}
	job := domain.AlertJob{ID: "job-1", OrganizationID: "org-1", ReadingID: reading.ID, Status: domain.JobPending, MaxAttempts: 2, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	if err := InsertAlertJob(ctx, store.DB(), job); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimAlertJob(ctx, "worker", "token-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAlertJob(ctx, claimed, errors.New("dependency down"), now); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempt int
	if err := store.DB().QueryRow(`SELECT status, attempt_count FROM alert_jobs WHERE id = 'job-1'`).Scan(&status, &attempt); err != nil {
		t.Fatal(err)
	}
	if status != "retry" || attempt != 1 {
		t.Fatalf("after first failure status=%s attempt=%d", status, attempt)
	}
	available := now.Add(domain.RetryDelay(1))
	claimed, err = store.ClaimAlertJob(ctx, "worker", "token-2", available, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAlertJob(ctx, claimed, errors.New("still down"), available); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT status, attempt_count FROM alert_jobs WHERE id = 'job-1'`).Scan(&status, &attempt); err != nil {
		t.Fatal(err)
	}
	if status != "dead" || attempt != 2 {
		t.Fatalf("after final failure status=%s attempt=%d", status, attempt)
	}
}
