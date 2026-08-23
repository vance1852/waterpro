package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/telemetry"
)

func TestWPAlertStaleCompletion(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	now := time.Now().UTC()
	if _, _, err := f.telemetry.Ingest(context.Background(), f.field, telemetry.IngestCommand{StationID: graph.station.ID, ExternalID: "wp-stale-alert", Parameter: "turbidity", Value: 20, Unit: "NTU", Threshold: 5, ObservedAt: now, RequestID: "wp-stale-alert"}); err != nil { t.Fatal(err) }
	first, err := f.store.ClaimAlertJob(context.Background(), "worker-1", "token-1", now, time.Second)
	if err != nil { t.Fatal(err) }
	if err := f.store.FinishAlertJob(context.Background(), first, errors.New("retry"), now); err != nil { t.Fatal(err) }
	second, err := f.store.ClaimAlertJob(context.Background(), "worker-1", "token-2", now.Add(time.Hour), time.Minute)
	if err != nil { t.Fatal(err) }
	if err := f.store.FinishAlertJob(context.Background(), first, nil, now.Add(time.Hour)); !errors.Is(err, domain.ErrLeaseLost) { t.Fatalf("stale completion error = %v", err) }
	var status, token string
	if err := f.store.DB().QueryRow(`SELECT status, lease_token FROM alert_jobs WHERE id = ?`, second.ID).Scan(&status, &token); err != nil { t.Fatal(err) }
	if status != "running" || token != "token-2" { t.Fatalf("new lease overwritten status=%s token=%s", status, token) }
}
