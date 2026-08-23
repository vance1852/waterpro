package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/incident"
)

func TestWPIncidentExpiredLease(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	reported, err := f.incidents.Report(context.Background(), f.field, incident.ReportCommand{SourceID: graph.source.ID, Title: "Expired command lease", Description: "A response lease expired before resource assignment", Severity: domain.SeveritySignificant, RequestID: "wp-expired-report"})
	if err != nil { t.Fatal(err) }
	claimed, err := f.incidents.Claim(context.Background(), f.supervisor, reported.ID)
	if err != nil { t.Fatal(err) }
	if _, err := f.store.DB().Exec(`UPDATE incidents SET lease_expires_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), reported.ID); err != nil { t.Fatal(err) }
	_, err = f.incidents.AssignContainment(context.Background(), f.supervisor, incident.AssignCommand{IncidentID: reported.ID, LeaseToken: claimed.LeaseToken, ResourceCode: "WP-BOOM", AssigneeUserID: f.field.UserID, RequestID: "wp-expired-assign"})
	if !errors.Is(err, domain.ErrLeaseLost) { t.Fatalf("expired lease assignment error = %v", err) }
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM containment_assignments WHERE incident_id = ?`, reported.ID).Scan(&count); err != nil { t.Fatal(err) }
	if count != 0 { t.Fatalf("expired lease persisted assignments = %d", count) }
}
