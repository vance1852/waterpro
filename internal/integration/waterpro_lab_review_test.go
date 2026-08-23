package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
	"github.com/vance1852/waterpro/internal/laboratory"
)

func TestWPLabReviewVersion(t *testing.T) {
	f := newFixture(t)
	graph := f.createSourceGraph(t)
	sample := f.createReceivedSample(t, graph)
	ctx := context.Background()
	now := time.Now().UTC()
	result, err := f.lab.RecordResult(ctx, f.analyst, laboratory.RecordResultCommand{SampleID: sample.ID, Parameter: "lead", Value: 1, Unit: "mg/L", MethodCode: "ICP", DetectionLimit: .1, RegulatoryLimit: 2, MeasuredAt: now, RequestID: "wp-lab-record"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.lab.Submit(ctx, f.analyst, result.ID, "wp-lab-submit"); err != nil {
		t.Fatal(err)
	}
	stale, err := f.store.LabResult(ctx, f.store.DB(), "org-1", result.ID)
	if err != nil {
		t.Fatal(err)
	}
	transition := func() error {
		return f.store.WithTx(ctx, nil, func(tx *sql.Tx) error {
			return f.store.TransitionLabResult(ctx, tx, stale, domain.LabResultApproved, f.supervisor.UserID, now)
		})
	}
	if err := transition(); err != nil {
		t.Fatal(err)
	}
	if err := transition(); err == nil {
		t.Fatalf("second review with stale result unexpectedly succeeded")
	}
}
