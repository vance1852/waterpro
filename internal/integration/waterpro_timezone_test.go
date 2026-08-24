package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/sampling"
	"github.com/vance1852/waterpro/internal/source"
)

func TestWPWaterSourceTimezoneDrivesSampleSequence(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sourceValue, err := f.sources.RegisterWaterSource(ctx, f.supervisor, source.RegisterSourceCommand{
		Name: "West Reservoir", Kind: domain.SourceReservoir, Timezone: "America/Los_Angeles", RequestID: "wp-timezone-source",
	})
	if err != nil {
		t.Fatalf("register source: %v", err)
	}
	zone, err := f.sources.RegisterZone(ctx, f.supervisor, source.RegisterZoneCommand{
		SourceID: sourceValue.ID, Name: "West primary", Level: domain.ZonePrimary, AreaSquareMeters: 5000, RequestID: "wp-timezone-zone",
	})
	if err != nil {
		t.Fatalf("register zone: %v", err)
	}
	station, err := f.sources.RegisterStation(ctx, f.supervisor, source.RegisterStationCommand{
		SourceID: sourceValue.ID, ZoneID: zone.ID, Code: "NIGHT", Name: "Night station", Latitude: 31, Longitude: 121, RequestID: "wp-timezone-station",
	})
	if err != nil {
		t.Fatalf("register station: %v", err)
	}
	nextDay := time.Now().UTC().Add(24 * time.Hour)
	collectedAt := time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 0, 30, 0, 0, time.UTC)
	plan, err := f.sampling.CreatePlan(ctx, f.supervisor, sampling.CreatePlanCommand{
		SourceID: sourceValue.ID, StationID: station.ID, AssignedUserID: f.field.UserID,
		WindowStart: collectedAt.Add(-time.Hour), WindowEnd: collectedAt.Add(time.Hour), RequiredBottles: 1, RequestID: "wp-timezone-plan",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if err := f.sampling.PublishPlan(ctx, f.supervisor, plan.ID, "wp-timezone-publish"); err != nil {
		t.Fatalf("publish plan: %v", err)
	}
	sample, err := f.sampling.Collect(ctx, f.field, sampling.CollectCommand{
		PlanID: plan.ID, BottleCount: 1, CollectedAt: collectedAt, RequestID: "wp-timezone-collect",
	})
	if err != nil {
		t.Fatalf("collect sample: %v", err)
	}
	local, _ := time.LoadLocation("America/Los_Angeles")
	want := domain.FormatSampleLabel("NIGHT", collectedAt.In(local), 1)
	if sample.Label != want {
		t.Fatalf("sample label = %q, want local business day label %s", sample.Label, want)
	}
}
