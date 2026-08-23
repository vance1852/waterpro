package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vance1852/waterpro/internal/audit"
	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/sampling"
)

func TestWPPlanPublishVersion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	graph := f.createSourceGraph(t)
	now := time.Now().UTC()
	plan, err := f.sampling.CreatePlan(ctx, f.supervisor, sampling.CreatePlanCommand{
		SourceID: graph.source.ID, StationID: graph.station.ID, AssignedUserID: f.field.UserID,
		WindowStart: now.Add(-time.Hour), WindowEnd: now.Add(time.Hour), RequiredBottles: 1,
		RequestID: "wp-plan-create",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	stale, err := f.store.SamplingPlan(ctx, f.store.DB(), "org-1", plan.ID)
	if err != nil {
		t.Fatalf("read plan snapshot: %v", err)
	}
	publish := func(requestID string) error {
		return f.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
			if err := f.store.TransitionSamplingPlan(ctx, tx, stale, domain.PlanPublished, now); err != nil {
				return err
			}
			return audit.Insert(ctx, tx, domain.AuditEvent{
				ID: uuid.NewString(), OrganizationID: "org-1", ActorUserID: "supervisor", RequestID: requestID,
				Action: "sampling_plan.publish", ObjectType: "sampling_plan", ObjectID: stale.ID,
				Outcome: "success", Metadata: "{}", OccurredAt: now,
			})
		})
	}
	if err := publish("wp-plan-publish-1"); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if err := publish("wp-plan-publish-2"); err == nil {
		t.Fatalf("second publish with stale version unexpectedly succeeded")
	}
	var status string
	var version int64
	if err := f.store.DB().QueryRowContext(ctx, "SELECT status, version FROM sampling_plans WHERE id = ?", plan.ID).Scan(&status, &version); err != nil {
		t.Fatalf("read published plan: %v", err)
	}
	if status != string(domain.PlanPublished) || version != 2 {
		t.Fatalf("plan state = %s/%d, want published/2", status, version)
	}
	var audits int
	if err := f.store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE object_id = ? AND action = 'sampling_plan.publish'", plan.ID).Scan(&audits); err != nil {
		t.Fatalf("count publish audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("publish audits = %d, want 1", audits)
	}
}
