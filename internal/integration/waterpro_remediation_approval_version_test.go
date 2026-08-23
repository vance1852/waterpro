package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/incident"
	"github.com/vance1852/waterpro/internal/remediation"
)

func TestWPRemediationApprovalVersion(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	reported, err := f.incidents.Report(context.Background(), f.field, incident.ReportCommand{SourceID: graph.source.ID, Title: "Approval fencing", Description: "Remediation approval must use the current plan revision", Severity: domain.SeveritySignificant, RequestID: "wp-plan-report"})
	if err != nil { t.Fatal(err) }
	plan, err := f.remediation.CreatePlan(context.Background(), f.supervisor, remediation.CreatePlanCommand{IncidentID: reported.ID, Title: "Protected bank cleanup", Objective: "Remove deposited contaminant", BudgetCents: 10000, Actions: []remediation.CreateAction{{IdempotencyKey: "remove", Description: "Remove contaminated sediment"}}, RequestID: "wp-plan-create"})
	if err != nil { t.Fatal(err) }
	stale, err := f.store.RemediationPlan(context.Background(), f.store.DB(), "org-1", plan.ID)
	if err != nil { t.Fatal(err) }
	transition := func() error { return f.store.WithTx(context.Background(), nil, func(tx *sql.Tx) error { return f.store.TransitionRemediationPlan(context.Background(), tx, stale, domain.RemediationApproved, f.supervisor.UserID, time.Now().UTC()) }) }
	if err := transition(); err != nil { t.Fatal(err) }
	if err := transition(); err == nil { t.Fatal("stale remediation approval unexpectedly succeeded twice") }
}
