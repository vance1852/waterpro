package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/sampling"
)

func TestWPCustodyHandoffContext(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	now := time.Now().UTC()
	plan, err := f.sampling.CreatePlan(context.Background(), f.supervisor, sampling.CreatePlanCommand{SourceID: graph.source.ID, StationID: graph.station.ID, AssignedUserID: f.field.UserID, WindowStart: now.Add(-time.Hour), WindowEnd: now.Add(time.Hour), RequiredBottles: 1, RequestID: "wp-canceled-plan"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.sampling.PublishPlan(context.Background(), f.supervisor, plan.ID, "wp-canceled-publish"); err != nil {
		t.Fatal(err)
	}
	sample, err := f.sampling.Collect(context.Background(), f.field, sampling.CollectCommand{PlanID: plan.ID, BottleCount: 1, CollectedAt: now, RequestID: "wp-canceled-collect"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = f.sampling.Handoff(ctx, f.field, sampling.HandoffCommand{SampleID: sample.ID, ToUserID: f.supervisor.UserID, RequestID: "wp-canceled-handoff"})
	if err == nil {
		t.Fatalf("canceled handoff unexpectedly succeeded")
	}
	var count int
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM custody_events WHERE sample_id = ? AND request_id = ?", sample.ID, "wp-canceled-handoff").Scan(&count); err != nil {
		t.Fatalf("count custody events: %v", err)
	}
	if count != 0 {
		t.Fatalf("canceled handoff persisted %d custody events", count)
	}
}
