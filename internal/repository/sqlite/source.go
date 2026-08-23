package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vance1852/waterpro/internal/domain"
)

func InsertWaterSource(ctx context.Context, db DBTX, source domain.WaterSource) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO water_sources(
			id, organization_id, name, kind, timezone, active,
			version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		source.ID,
		source.OrganizationID,
		source.Name,
		string(source.Kind),
		source.Timezone,
		boolInt(source.Active),
		source.Version,
		formatTime(source.CreatedAt),
		formatTime(source.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert water source: %w", err)
	}
	return nil
}

func scanWaterSource(scanner interface{ Scan(...any) error }) (domain.WaterSource, error) {
	var source domain.WaterSource
	var kind, created, updated string
	var active int
	err := scanner.Scan(
		&source.ID,
		&source.OrganizationID,
		&source.Name,
		&kind,
		&source.Timezone,
		&active,
		&source.Version,
		&created,
		&updated,
	)
	if err != nil {
		return domain.WaterSource{}, err
	}
	source.Kind = domain.SourceKind(kind)
	source.Active = active == 1
	var parseErr error
	if source.CreatedAt, parseErr = parseTime(created); parseErr != nil {
		return domain.WaterSource{}, parseErr
	}
	if source.UpdatedAt, parseErr = parseTime(updated); parseErr != nil {
		return domain.WaterSource{}, parseErr
	}
	return source, nil
}

const selectSourceColumns = `id, organization_id, name, kind, timezone, active, version, created_at, updated_at`

func (s *Store) WaterSource(ctx context.Context, db DBTX, organizationID, sourceID string) (domain.WaterSource, error) {
	source, err := scanWaterSource(db.QueryRowContext(ctx, "SELECT "+selectSourceColumns+" FROM water_sources WHERE organization_id = ? AND id = ?", organizationID, sourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WaterSource{}, &domain.NotFoundError{Resource: "water source", ID: sourceID}
	}
	if err != nil {
		return domain.WaterSource{}, fmt.Errorf("select water source: %w", err)
	}
	return source, nil
}

func (s *Store) ListWaterSources(ctx context.Context, organizationID string, activeOnly bool, page domain.PageRequest) (domain.Page[domain.WaterSource], error) {
	page = page.Normalized()
	query := "SELECT " + selectSourceColumns + " FROM water_sources WHERE organization_id = ?"
	args := []any{organizationID}
	if activeOnly {
		query += " AND active = 1"
	}
	if page.Cursor != "" {
		query += " AND id > ?"
		args = append(args, page.Cursor)
	}
	query += " ORDER BY id ASC LIMIT ?"
	args = append(args, page.Limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return domain.Page[domain.WaterSource]{}, fmt.Errorf("query water sources: %w", err)
	}
	defer rows.Close()
	items := make([]domain.WaterSource, 0, page.Limit)
	for rows.Next() {
		source, err := scanWaterSource(rows)
		if err != nil {
			return domain.Page[domain.WaterSource]{}, fmt.Errorf("scan water source: %w", err)
		}
		items = append(items, source)
	}
	if err := rows.Err(); err != nil {
		return domain.Page[domain.WaterSource]{}, fmt.Errorf("iterate water sources: %w", err)
	}
	result := domain.Page[domain.WaterSource]{Items: items}
	if len(items) > page.Limit {
		result.NextCursor = items[page.Limit-1].ID
		result.Items = items[:page.Limit]
	}
	return result, nil
}

func InsertProtectionZone(ctx context.Context, db DBTX, zone domain.ProtectionZone) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO protection_zones(
			id, source_id, organization_id, name, level, area_square_meters,
			active, version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		zone.ID,
		zone.SourceID,
		zone.OrganizationID,
		zone.Name,
		string(zone.Level),
		zone.AreaSquareMeters,
		boolInt(zone.Active),
		zone.Version,
		formatTime(zone.CreatedAt),
		formatTime(zone.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert protection zone: %w", err)
	}
	return nil
}

func (s *Store) ProtectionZone(ctx context.Context, db DBTX, organizationID, zoneID string) (domain.ProtectionZone, error) {
	var zone domain.ProtectionZone
	var level, created, updated string
	var active int
	err := db.QueryRowContext(ctx, `
		SELECT id, source_id, organization_id, name, level, area_square_meters,
		       active, version, created_at, updated_at
		FROM protection_zones
		WHERE organization_id = ? AND id = ?`, organizationID, zoneID).Scan(
		&zone.ID,
		&zone.SourceID,
		&zone.OrganizationID,
		&zone.Name,
		&level,
		&zone.AreaSquareMeters,
		&active,
		&zone.Version,
		&created,
		&updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProtectionZone{}, &domain.NotFoundError{Resource: "protection zone", ID: zoneID}
	}
	if err != nil {
		return domain.ProtectionZone{}, fmt.Errorf("select protection zone: %w", err)
	}
	zone.Level = domain.ProtectionZoneLevel(level)
	zone.Active = active == 1
	if zone.CreatedAt, err = parseTime(created); err != nil {
		return domain.ProtectionZone{}, err
	}
	if zone.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.ProtectionZone{}, err
	}
	return zone, nil
}

func InsertMonitoringStation(ctx context.Context, db DBTX, station domain.MonitoringStation) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO monitoring_stations(
			id, source_id, zone_id, organization_id, code, name,
			latitude, longitude, active, version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		station.ID,
		station.SourceID,
		station.ZoneID,
		station.OrganizationID,
		strings.ToUpper(station.Code),
		station.Name,
		station.Latitude,
		station.Longitude,
		boolInt(station.Active),
		station.Version,
		formatTime(station.CreatedAt),
		formatTime(station.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert monitoring station: %w", err)
	}
	return nil
}

func (s *Store) MonitoringStation(ctx context.Context, db DBTX, organizationID, stationID string) (domain.MonitoringStation, error) {
	var station domain.MonitoringStation
	var active int
	var created, updated string
	err := db.QueryRowContext(ctx, `
		SELECT id, source_id, zone_id, organization_id, code, name,
		       latitude, longitude, active, version, created_at, updated_at
		FROM monitoring_stations
		WHERE organization_id = ? AND id = ?`, organizationID, stationID).Scan(
		&station.ID,
		&station.SourceID,
		&station.ZoneID,
		&station.OrganizationID,
		&station.Code,
		&station.Name,
		&station.Latitude,
		&station.Longitude,
		&active,
		&station.Version,
		&created,
		&updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MonitoringStation{}, &domain.NotFoundError{Resource: "monitoring station", ID: stationID}
	}
	if err != nil {
		return domain.MonitoringStation{}, fmt.Errorf("select monitoring station: %w", err)
	}
	station.Active = active == 1
	if station.CreatedAt, err = parseTime(created); err != nil {
		return domain.MonitoringStation{}, err
	}
	if station.UpdatedAt, err = parseTime(updated); err != nil {
		return domain.MonitoringStation{}, err
	}
	return station, nil
}

func (s *Store) ListStations(ctx context.Context, organizationID, sourceID string, activeOnly bool) ([]domain.MonitoringStation, error) {
	query := `
		SELECT id, source_id, zone_id, organization_id, code, name,
		       latitude, longitude, active, version, created_at, updated_at
		FROM monitoring_stations
		WHERE organization_id = ? AND source_id = ?`
	if activeOnly {
		query += " AND active = 1"
	}
	query += " ORDER BY code ASC"
	rows, err := s.db.QueryContext(ctx, query, organizationID, sourceID)
	if err != nil {
		return nil, fmt.Errorf("query monitoring stations: %w", err)
	}
	defer rows.Close()
	stations := make([]domain.MonitoringStation, 0)
	for rows.Next() {
		var station domain.MonitoringStation
		var active int
		var created, updated string
		if err := rows.Scan(
			&station.ID,
			&station.SourceID,
			&station.ZoneID,
			&station.OrganizationID,
			&station.Code,
			&station.Name,
			&station.Latitude,
			&station.Longitude,
			&active,
			&station.Version,
			&created,
			&updated,
		); err != nil {
			return nil, fmt.Errorf("scan monitoring station: %w", err)
		}
		station.Active = active == 1
		station.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		station.UpdatedAt, err = parseTime(updated)
		if err != nil {
			return nil, err
		}
		stations = append(stations, station)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monitoring stations: %w", err)
	}
	return stations, nil
}

func (s *Store) UpdateSourceActive(ctx context.Context, organizationID, sourceID string, expectedVersion int64, active bool, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE water_sources
		SET active = ?, version = version + 1, updated_at = ?
		WHERE organization_id = ? AND id = ? AND version = ?`,
		boolInt(active), formatTime(now), organizationID, sourceID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update source active state: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read source update count: %w", err)
	}
	if changed != 1 {
		return &domain.ConflictError{Resource: "water source", Key: sourceID, Cause: domain.ErrConflict}
	}
	return nil
}
