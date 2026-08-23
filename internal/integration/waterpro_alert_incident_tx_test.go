package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/telemetry"
)

func TestWPExceedanceIncidentTx(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	reading, _, err := f.telemetry.Ingest(context.Background(), f.field, telemetry.IngestCommand{
		StationID: graph.station.ID, ExternalID: "wp-alert-atomic", Parameter: "turbidity", Value: 20,
		Unit: "NTU", Threshold: 5, ObservedAt: time.Now().UTC(), RequestID: "wp-alert-atomic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`DROP TABLE outbox_events`); err != nil {
		t.Fatal(err)
	}
	err = telemetry.NewAlertProcessor(f.store).ProcessAlert(context.Background(), domain.AlertJob{
		ID: "wp-alert-job", OrganizationID: "org-1", ReadingID: reading.ID,
	})
	if err == nil {
		t.Fatal("alert processing unexpectedly succeeded without durable outbox")
	}
	var incidents int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM incidents WHERE originating_result_id IS NULL`).Scan(&incidents); err != nil {
		t.Fatal(err)
	}
	if incidents != 0 {
		t.Fatalf("incident persisted after outbox failure: %d", incidents)
	}
}
