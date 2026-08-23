package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func InsertSamplingPlan(ctx context.Context, db DBTX, plan domain.SamplingPlan) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO sampling_plans(
			id, organization_id, source_id, station_id, assigned_user_id,
			window_start, window_end, required_bottles, status, version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		plan.ID, plan.OrganizationID, plan.SourceID, plan.StationID, plan.AssignedUserID,
		formatTime(plan.WindowStart), formatTime(plan.WindowEnd), plan.RequiredBottles,
		string(plan.Status), plan.Version, formatTime(plan.CreatedAt), formatTime(plan.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert sampling plan: %w", err)
	}
	return nil
}

func scanSamplingPlan(scanner interface{ Scan(...any) error }) (domain.SamplingPlan, error) {
	var plan domain.SamplingPlan
	var windowStart, windowEnd, status, created, updated string
	err := scanner.Scan(
		&plan.ID, &plan.OrganizationID, &plan.SourceID, &plan.StationID, &plan.AssignedUserID,
		&windowStart, &windowEnd, &plan.RequiredBottles, &status, &plan.Version, &created, &updated,
	)
	if err != nil {
		return domain.SamplingPlan{}, err
	}
	var parseErr error
	if plan.WindowStart, parseErr = parseTime(windowStart); parseErr != nil {
		return domain.SamplingPlan{}, parseErr
	}
	if plan.WindowEnd, parseErr = parseTime(windowEnd); parseErr != nil {
		return domain.SamplingPlan{}, parseErr
	}
	if plan.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.SamplingPlan{}, parseErr
	}
	if plan.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.SamplingPlan{}, parseErr
	}
	plan.Status = domain.SamplingPlanStatus(status)
	return plan, nil
}

const selectSamplingPlan = `
	id, organization_id, source_id, station_id, assigned_user_id,
	window_start, window_end, required_bottles, status, version, created_at, updated_at`

func (s *Store) SamplingPlan(ctx context.Context, db DBTX, organizationID, planID string) (domain.SamplingPlan, error) {
	plan, err := scanSamplingPlan(db.QueryRowContext(ctx, "SELECT "+selectSamplingPlan+" FROM sampling_plans WHERE organization_id = ? AND id = ?", organizationID, planID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SamplingPlan{}, &domain.NotFoundError{Resource: "sampling plan", ID: planID}
	}
	if err != nil {
		return domain.SamplingPlan{}, fmt.Errorf("select sampling plan: %w", err)
	}
	return plan, nil
}

func (s *Store) CountOverlappingPlans(ctx context.Context, db DBTX, stationID string, start, end time.Time, excludeID string) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sampling_plans
		WHERE station_id = ?
		  AND id <> ?
		  AND status IN ('draft','published')
		  AND window_start < ?
		  AND window_end > ?`, stationID, excludeID, formatTime(end), formatTime(start)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count overlapping plans: %w", err)
	}
	return count, nil
}

func (s *Store) TransitionSamplingPlan(ctx context.Context, tx *sql.Tx, plan domain.SamplingPlan, to domain.SamplingPlanStatus, now time.Time) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE sampling_plans
		SET status = ?, version = version + 1, updated_at = ?
		WHERE organization_id = ? AND id = ? AND version = ? AND status = ?`,
		string(to), formatTime(now), plan.OrganizationID, plan.ID, plan.Version, string(plan.Status))
	if err != nil {
		return fmt.Errorf("transition sampling plan: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read sampling plan transition count: %w", err)
	}
	if changed != 1 {
		return &domain.ConflictError{Resource: "sampling plan", Key: plan.ID, Cause: domain.ErrConflict}
	}
	return nil
}

func (s *Store) NextSampleSequence(ctx context.Context, tx *sql.Tx, organizationID, stationID, businessDay string) (int64, error) {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO sample_sequences(organization_id, station_id, business_day, next_value, version)
		VALUES (?, ?, ?, 1, 1)
		ON CONFLICT(organization_id, station_id, business_day) DO NOTHING`, organizationID, stationID, businessDay)
	if err != nil {
		return 0, fmt.Errorf("ensure sample sequence: %w", err)
	}
	var next int64
	var version int64
	if err := tx.QueryRowContext(ctx, `
		SELECT next_value, version
		FROM sample_sequences
		WHERE organization_id = ? AND station_id = ? AND business_day = ?`, organizationID, stationID, businessDay).Scan(&next, &version); err != nil {
		return 0, fmt.Errorf("read sample sequence: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sample_sequences
		SET next_value = ?, version = version + 1
		WHERE organization_id = ? AND station_id = ? AND business_day = ? AND version = ?`, next+1, organizationID, stationID, businessDay, version)
	if err != nil {
		return 0, fmt.Errorf("advance sample sequence: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read sample sequence update count: %w", err)
	}
	if changed != 1 {
		return 0, &domain.ConflictError{Resource: "sample sequence", Key: businessDay, Cause: domain.ErrConflict}
	}
	return next, nil
}

func InsertSample(ctx context.Context, db DBTX, sample domain.Sample) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO samples(
			id, organization_id, plan_id, station_id, sequence, label, bottle_count,
			status, custodian_user_id, collected_at, received_at, version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sample.ID, sample.OrganizationID, sample.PlanID, sample.StationID, sample.Sequence,
		sample.Label, sample.BottleCount, string(sample.Status), sample.CustodianUserID,
		nullableTime(sample.CollectedAt), nullableTime(sample.ReceivedAt), sample.Version,
		formatTime(sample.CreatedAt), formatTime(sample.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert sample: %w", err)
	}
	return nil
}

func scanSample(scanner interface{ Scan(...any) error }) (domain.Sample, error) {
	var sample domain.Sample
	var status, created, updated string
	var collected, received sql.NullString
	err := scanner.Scan(
		&sample.ID, &sample.OrganizationID, &sample.PlanID, &sample.StationID, &sample.Sequence,
		&sample.Label, &sample.BottleCount, &status, &sample.CustodianUserID,
		&collected, &received, &sample.Version, &created, &updated,
	)
	if err != nil {
		return domain.Sample{}, err
	}
	sample.Status = domain.SampleStatus(status)
	if collected.Valid {
		value, err := parseTime(collected.String)
		if err != nil {
			return domain.Sample{}, err
		}
		sample.CollectedAt = &value
	}
	if received.Valid {
		value, err := parseTime(received.String)
		if err != nil {
			return domain.Sample{}, err
		}
		sample.ReceivedAt = &value
	}
	var parseErr error
	if sample.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.Sample{}, parseErr
	}
	if sample.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.Sample{}, parseErr
	}
	return sample, nil
}

const selectSample = `
	id, organization_id, plan_id, station_id, sequence, label, bottle_count,
	status, custodian_user_id, collected_at, received_at, version, created_at, updated_at`

func (s *Store) Sample(ctx context.Context, db DBTX, organizationID, sampleID string) (domain.Sample, error) {
	sample, err := scanSample(db.QueryRowContext(ctx, "SELECT "+selectSample+" FROM samples WHERE organization_id = ? AND id = ?", organizationID, sampleID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Sample{}, &domain.NotFoundError{Resource: "sample", ID: sampleID}
	}
	if err != nil {
		return domain.Sample{}, fmt.Errorf("select sample: %w", err)
	}
	return sample, nil
}

func (s *Store) TransitionSample(ctx context.Context, tx *sql.Tx, sample domain.Sample, to domain.SampleStatus, custodian string, occurredAt time.Time) error {
	var collectedAt, receivedAt any
	if sample.CollectedAt != nil {
		collectedAt = formatTime(*sample.CollectedAt)
	}
	if sample.ReceivedAt != nil {
		receivedAt = formatTime(*sample.ReceivedAt)
	}
	if to == domain.SampleCollected {
		collectedAt = formatTime(occurredAt)
	}
	if to == domain.SampleReceived {
		receivedAt = formatTime(occurredAt)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE samples
		SET status = ?, custodian_user_id = ?, collected_at = ?, received_at = ?,
		    version = version + 1, updated_at = ?
		WHERE organization_id = ? AND id = ? AND status = ? AND version = ?`,
		string(to), custodian, collectedAt, receivedAt, formatTime(occurredAt),
		sample.OrganizationID, sample.ID, string(sample.Status), sample.Version)
	if err != nil {
		return fmt.Errorf("transition sample: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read sample transition count: %w", err)
	}
	if changed != 1 {
		return &domain.ConflictError{Resource: "sample", Key: sample.ID, Cause: domain.ErrConflict}
	}
	return nil
}

func InsertCustodyEvent(ctx context.Context, db DBTX, event domain.CustodyEvent) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO custody_events(
			id, organization_id, sample_id, from_user_id, to_user_id,
			action, occurred_at, request_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.OrganizationID, event.SampleID, nullString(event.FromUserID),
		event.ToUserID, event.Action, formatTime(event.OccurredAt), event.RequestID,
	)
	if err != nil {
		return fmt.Errorf("insert custody event: %w", err)
	}
	return nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Store) ListCustodyEvents(ctx context.Context, organizationID, sampleID string) ([]domain.CustodyEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, organization_id, sample_id, from_user_id, to_user_id, action, occurred_at, request_id
		FROM custody_events
		WHERE organization_id = ? AND sample_id = ?
		ORDER BY occurred_at ASC, id ASC`, organizationID, sampleID)
	if err != nil {
		return nil, fmt.Errorf("query custody events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.CustodyEvent, 0)
	for rows.Next() {
		var event domain.CustodyEvent
		var from sql.NullString
		var occurred string
		if err := rows.Scan(&event.ID, &event.OrganizationID, &event.SampleID, &from, &event.ToUserID, &event.Action, &occurred, &event.RequestID); err != nil {
			return nil, fmt.Errorf("scan custody event: %w", err)
		}
		if from.Valid {
			event.FromUserID = from.String
		}
		event.OccurredAt, err = parseTime(occurred)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate custody events: %w", err)
	}
	return events, nil
}
