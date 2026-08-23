package telemetry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	repository "github.com/vance1852/waterpro/internal/repository/sqlite"
)

func TestAlertProcessorCreatesIncidentOutboxAndAuditAtomically(t *testing.T) {
	store, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO organizations(id, name, created_at) VALUES ('org', 'Authority', ?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO water_sources(id, organization_id, name, kind, timezone, active, version, created_at, updated_at) VALUES ('src', 'org', 'Reservoir', 'reservoir', 'UTC', 1, 1, ?, ?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO protection_zones(id, source_id, organization_id, name, level, area_square_meters, active, version, created_at, updated_at) VALUES ('zone', 'src', 'org', 'Primary', 'primary', 100, 1, 1, ?, ?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO monitoring_stations(id, source_id, zone_id, organization_id, code, name, latitude, longitude, active, version, created_at, updated_at) VALUES ('station', 'src', 'zone', 'org', 'S1', 'Station', 0, 0, 1, 1, ?, ?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	reading := domain.TelemetryReading{ID: "reading", OrganizationID: "org", StationID: "station", ExternalID: "external", Parameter: "turbidity", Value: 12, Unit: "NTU", Threshold: 5, ObservedAt: now, ReceivedAt: now}
	if created, err := repository.InsertTelemetryReading(ctx, store.DB(), reading); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	job := domain.AlertJob{ID: "job", OrganizationID: "org", ReadingID: reading.ID, Status: domain.JobRunning, MaxAttempts: 5, LeaseOwner: "worker", LeaseToken: "token", LeaseGeneration: 1, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	processor := NewAlertProcessor(store)
	processor.clock = func() time.Time { return now }
	if err := processor.ProcessAlert(ctx, job); err != nil {
		t.Fatalf("ProcessAlert() error = %v", err)
	}
	var incidents, outbox, audits int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM incidents WHERE source_id = 'src' AND severity = 'critical'`).Scan(&incidents); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE idempotency_key = 'telemetry:reading'`).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE request_id = 'alert-job:job'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if incidents != 1 || outbox != 1 || audits != 1 {
		t.Fatalf("incidents=%d outbox=%d audits=%d", incidents, outbox, audits)
	}
}

func TestAlertProcessorIgnoresReadingBelowThreshold(t *testing.T) {
	store, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "below.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO organizations(id, name, created_at) VALUES ('org', 'Authority', ?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO water_sources(id, organization_id, name, kind, timezone, active, version, created_at, updated_at) VALUES ('src', 'org', 'Reservoir', 'reservoir', 'UTC', 1, 1, ?, ?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO protection_zones(id, source_id, organization_id, name, level, area_square_meters, active, version, created_at, updated_at) VALUES ('zone', 'src', 'org', 'Primary', 'primary', 100, 1, 1, ?, ?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO monitoring_stations(id, source_id, zone_id, organization_id, code, name, latitude, longitude, active, version, created_at, updated_at) VALUES ('station', 'src', 'zone', 'org', 'S1', 'Station', 0, 0, 1, 1, ?, ?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	reading := domain.TelemetryReading{ID: "reading", OrganizationID: "org", StationID: "station", ExternalID: "external", Parameter: "pH", Value: 7, Unit: "pH", Threshold: 8, ObservedAt: now, ReceivedAt: now}
	if created, err := repository.InsertTelemetryReading(ctx, store.DB(), reading); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if err := NewAlertProcessor(store).ProcessAlert(ctx, domain.AlertJob{ID: "job", OrganizationID: "org", ReadingID: reading.ID}); err != nil {
		t.Fatal(err)
	}
	var incidents int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM incidents`).Scan(&incidents); err != nil {
		t.Fatal(err)
	}
	if incidents != 0 {
		t.Fatalf("below-threshold processing created %d incidents", incidents)
	}
}
