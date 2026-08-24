package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/sampling"
)

func TestWPUnauthorizedHandoff(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	now := time.Now().UTC()
	plan, err := f.sampling.CreatePlan(context.Background(), f.supervisor, sampling.CreatePlanCommand{SourceID: graph.source.ID, StationID: graph.station.ID, AssignedUserID: f.field.UserID, WindowStart: now.Add(-time.Hour), WindowEnd: now.Add(time.Hour), RequiredBottles: 2, RequestID: "wp-unauth-plan"})
	if err != nil { t.Fatal(err) }
	if err := f.sampling.PublishPlan(context.Background(), f.supervisor, plan.ID, "wp-unauth-publish"); err != nil { t.Fatal(err) }
	sample, err := f.sampling.Collect(context.Background(), f.field, sampling.CollectCommand{PlanID: plan.ID, BottleCount: 2, CollectedAt: now, RequestID: "wp-unauth-collect"})
	if err != nil { t.Fatal(err) }
	_, err = f.sampling.Handoff(context.Background(), f.analyst, sampling.HandoffCommand{SampleID: sample.ID, ToUserID: f.field.UserID, OccurredAt: now, RequestID: "wp-unauthorized-handoff"})
	if !errors.Is(err, domain.ErrForbidden) { t.Fatalf("unauthorized handoff error = %v", err) }
	var events int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM custody_events WHERE sample_id = ? AND request_id = 'wp-unauthorized-handoff'`, sample.ID).Scan(&events); err != nil { t.Fatal(err) }
	if events != 0 { t.Fatalf("unauthorized handoff wrote %d custody events", events) }
}
