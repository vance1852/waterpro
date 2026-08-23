PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE organizations (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('field_operator','lab_analyst','protection_supervisor')),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    auth_generation INTEGER NOT NULL DEFAULT 1 CHECK (auth_generation > 0),
    failed_login_count INTEGER NOT NULL DEFAULT 0 CHECK (failed_login_count >= 0),
    locked_until TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (organization_id, email)
);
CREATE INDEX idx_users_org_active ON users(organization_id, active);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    auth_generation INTEGER NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_user_active ON sessions(user_id, revoked_at, expires_at);

CREATE TABLE water_sources (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('reservoir','river_intake','groundwater')),
    timezone TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (organization_id, name)
);
CREATE INDEX idx_sources_org_active ON water_sources(organization_id, active);

CREATE TABLE protection_zones (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES water_sources(id),
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    name TEXT NOT NULL,
    level TEXT NOT NULL CHECK (level IN ('primary','secondary','buffer')),
    area_square_meters INTEGER NOT NULL CHECK (area_square_meters > 0),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (source_id, name),
    UNIQUE (id, organization_id)
);
CREATE INDEX idx_zones_source_level ON protection_zones(source_id, level, active);

CREATE TABLE monitoring_stations (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES water_sources(id),
    zone_id TEXT NOT NULL REFERENCES protection_zones(id),
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    latitude REAL NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude REAL NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (organization_id, code),
    UNIQUE (id, organization_id)
);
CREATE INDEX idx_stations_source_active ON monitoring_stations(source_id, active);

CREATE TABLE sampling_plans (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    source_id TEXT NOT NULL REFERENCES water_sources(id),
    station_id TEXT NOT NULL REFERENCES monitoring_stations(id),
    assigned_user_id TEXT NOT NULL REFERENCES users(id),
    window_start TEXT NOT NULL,
    window_end TEXT NOT NULL,
    required_bottles INTEGER NOT NULL CHECK (required_bottles BETWEEN 1 AND 24),
    status TEXT NOT NULL CHECK (status IN ('draft','published','completed','cancelled')),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (window_end > window_start),
    UNIQUE (id, organization_id)
);
CREATE INDEX idx_sampling_plans_station_window ON sampling_plans(station_id, window_start, window_end, status);
CREATE INDEX idx_sampling_plans_assignee ON sampling_plans(assigned_user_id, status, window_start);

CREATE TABLE sample_sequences (
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    station_id TEXT NOT NULL REFERENCES monitoring_stations(id),
    business_day TEXT NOT NULL,
    next_value INTEGER NOT NULL CHECK (next_value > 0),
    version INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (organization_id, station_id, business_day)
);

CREATE TABLE samples (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    plan_id TEXT NOT NULL REFERENCES sampling_plans(id),
    station_id TEXT NOT NULL REFERENCES monitoring_stations(id),
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    label TEXT NOT NULL,
    bottle_count INTEGER NOT NULL CHECK (bottle_count > 0),
    status TEXT NOT NULL CHECK (status IN ('planned','collected','in_transit','received','tested','archived','rejected')),
    custodian_user_id TEXT NOT NULL REFERENCES users(id),
    collected_at TEXT,
    received_at TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (organization_id, label),
    UNIQUE (station_id, sequence, created_at),
    UNIQUE (id, organization_id)
);
CREATE INDEX idx_samples_plan_status ON samples(plan_id, status);
CREATE INDEX idx_samples_custodian_status ON samples(custodian_user_id, status);

CREATE TABLE custody_events (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    sample_id TEXT NOT NULL REFERENCES samples(id),
    from_user_id TEXT REFERENCES users(id),
    to_user_id TEXT NOT NULL REFERENCES users(id),
    action TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    request_id TEXT NOT NULL,
    UNIQUE (sample_id, request_id, action)
);
CREATE INDEX idx_custody_sample_time ON custody_events(sample_id, occurred_at, id);

CREATE TABLE lab_results (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    sample_id TEXT NOT NULL REFERENCES samples(id),
    parameter TEXT NOT NULL,
    value REAL NOT NULL CHECK (value >= 0),
    unit TEXT NOT NULL,
    method_code TEXT NOT NULL,
    detection_limit REAL NOT NULL CHECK (detection_limit >= 0),
    regulatory_limit REAL NOT NULL CHECK (regulatory_limit > 0),
    status TEXT NOT NULL CHECK (status IN ('draft','submitted','approved','rejected')),
    analyst_user_id TEXT NOT NULL REFERENCES users(id),
    reviewer_user_id TEXT REFERENCES users(id),
    version INTEGER NOT NULL DEFAULT 1,
    measured_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (sample_id, parameter, method_code),
    UNIQUE (id, organization_id)
);
CREATE INDEX idx_lab_results_sample_status ON lab_results(sample_id, status);

CREATE TABLE incidents (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    source_id TEXT NOT NULL REFERENCES water_sources(id),
    originating_result_id TEXT REFERENCES lab_results(id),
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    severity TEXT NOT NULL CHECK (severity IN ('advisory','significant','critical')),
    status TEXT NOT NULL CHECK (status IN ('reported','assessing','contained','remediating','resolved')),
    commander_user_id TEXT REFERENCES users(id),
    lease_token TEXT,
    lease_generation INTEGER NOT NULL DEFAULT 0,
    lease_expires_at TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    reported_at TEXT NOT NULL,
    resolved_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (originating_result_id),
    UNIQUE (id, organization_id)
);
CREATE INDEX idx_incidents_source_status ON incidents(source_id, status, severity);
CREATE INDEX idx_incidents_lease ON incidents(status, lease_expires_at);

CREATE TABLE containment_assignments (
    id TEXT PRIMARY KEY,
    incident_id TEXT NOT NULL REFERENCES incidents(id),
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    resource_code TEXT NOT NULL,
    assignee_user_id TEXT NOT NULL REFERENCES users(id),
    status TEXT NOT NULL CHECK (status IN ('pending','active','completed','cancelled')),
    lease_token TEXT,
    lease_generation INTEGER NOT NULL DEFAULT 0,
    lease_expires_at TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (organization_id, resource_code, status),
    UNIQUE (id, organization_id)
);
CREATE INDEX idx_assignments_incident_status ON containment_assignments(incident_id, status);

CREATE TABLE remediation_plans (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    incident_id TEXT NOT NULL REFERENCES incidents(id),
    title TEXT NOT NULL,
    objective TEXT NOT NULL,
    budget_cents INTEGER NOT NULL CHECK (budget_cents > 0),
    status TEXT NOT NULL CHECK (status IN ('draft','approved','executing','verified','closed')),
    approved_by TEXT REFERENCES users(id),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (incident_id),
    UNIQUE (id, organization_id)
);

CREATE TABLE remediation_actions (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL REFERENCES remediation_plans(id),
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    idempotency_key TEXT NOT NULL,
    description TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','in_progress','succeeded','failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (organization_id, plan_id, idempotency_key),
    UNIQUE (id, organization_id)
);
CREATE INDEX idx_remediation_actions_plan_status ON remediation_actions(plan_id, status);

CREATE TABLE permits (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    source_id TEXT NOT NULL REFERENCES water_sources(id),
    holder_name TEXT NOT NULL,
    reference TEXT NOT NULL,
    valid_from TEXT NOT NULL,
    valid_until TEXT NOT NULL,
    daily_volume_limit_liters INTEGER NOT NULL CHECK (daily_volume_limit_liters > 0),
    status TEXT NOT NULL CHECK (status IN ('draft','active','suspended','expired','revoked')),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (valid_until > valid_from),
    UNIQUE (organization_id, reference),
    UNIQUE (id, organization_id)
);
CREATE INDEX idx_permits_source_status ON permits(source_id, status, valid_until);

CREATE TABLE discharge_events (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    permit_id TEXT NOT NULL REFERENCES permits(id),
    idempotency_key TEXT NOT NULL,
    volume_liters INTEGER NOT NULL CHECK (volume_liters > 0),
    occurred_at TEXT NOT NULL,
    reported_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    UNIQUE (organization_id, permit_id, idempotency_key)
);
CREATE INDEX idx_discharge_permit_day ON discharge_events(permit_id, occurred_at);

CREATE TABLE telemetry_readings (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    station_id TEXT NOT NULL REFERENCES monitoring_stations(id),
    external_id TEXT NOT NULL,
    parameter TEXT NOT NULL,
    value REAL NOT NULL,
    unit TEXT NOT NULL,
    threshold REAL NOT NULL,
    observed_at TEXT NOT NULL,
    received_at TEXT NOT NULL,
    UNIQUE (organization_id, station_id, external_id)
);
CREATE INDEX idx_telemetry_station_parameter_time ON telemetry_readings(station_id, parameter, observed_at DESC);

CREATE TABLE alert_jobs (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    reading_id TEXT NOT NULL REFERENCES telemetry_readings(id),
    status TEXT NOT NULL CHECK (status IN ('pending','running','retry','succeeded','dead')),
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
    available_at TEXT NOT NULL,
    lease_owner TEXT,
    lease_token TEXT,
    lease_generation INTEGER NOT NULL DEFAULT 0,
    lease_expires_at TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (reading_id)
);
CREATE INDEX idx_alert_jobs_claim ON alert_jobs(status, available_at, lease_expires_at);

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    actor_user_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    action TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    outcome TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    occurred_at TEXT NOT NULL
);
CREATE INDEX idx_audit_object_time ON audit_events(organization_id, object_type, object_id, occurred_at);
CREATE INDEX idx_audit_request ON audit_events(request_id);

CREATE TABLE outbox_events (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    topic TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload BLOB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','sending','retry','delivered','dead')),
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
    available_at TEXT NOT NULL,
    lease_owner TEXT,
    lease_token TEXT,
    lease_generation INTEGER NOT NULL DEFAULT 0,
    lease_expires_at TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (organization_id, topic, idempotency_key)
);
CREATE INDEX idx_outbox_claim ON outbox_events(status, available_at, lease_expires_at);

CREATE TABLE idempotency_records (
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    actor_user_id TEXT NOT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_status INTEGER,
    response_body BLOB,
    state TEXT NOT NULL CHECK (state IN ('running','completed','failed')),
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (organization_id, actor_user_id, method, path, idempotency_key)
);
CREATE INDEX idx_idempotency_expiry ON idempotency_records(expires_at);
