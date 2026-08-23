# waterpro

waterpro is a production-oriented Go backend for drinking-water source protection operations. It coordinates source and protection-zone registration, monitoring stations, field sampling and chain of custody, laboratory review, permit controls, pollution incident response, remediation, telemetry alerts, audit records, and reliable background delivery.

The repository is intentionally a backend application rather than a dashboard or generic CRUD scaffold. Its public workflows enforce cross-entity state, ownership, transaction, concurrency, time, and authorization invariants through real SQLite persistence.

## Runtime

- Go 1.22 (`GOTOOLCHAIN=local` is used by the verification commands).
- SQLite through the pure-Go `modernc.org/sqlite` driver.
- `cmd/server` serves HTTP and runs the alert/outbox workers under one cancellation lifecycle.
- The default address is `:8080`; the default database is `waterpro.db`.
- `GET /healthz` reports process liveness. `GET /readyz` checks the database and foreign-key enforcement.

Copy `.env.example` values into your process environment as needed. Do not commit local credentials. A first supervisor can be created at startup with all four variables below; bootstrap is idempotent and stores a bcrypt hash rather than the password.

```text
WATERPRO_BOOTSTRAP_ORG_ID=watershed-1
WATERPRO_BOOTSTRAP_ORG_NAME=Regional Watershed Authority
WATERPRO_BOOTSTRAP_EMAIL=supervisor@example.test
WATERPRO_BOOTSTRAP_PASSWORD=a-long-local-password
```

Start the service:

```bash
GOTOOLCHAIN=local go run ./cmd/server
```

The process handles `SIGINT` and `SIGTERM`, stops accepting HTTP requests, cancels worker loops, waits for in-flight requests within the configured shutdown timeout, and closes the database.

## Authentication

Clients log in through `POST /v1/auth/login` with `organization_id`, `email`, and `password`. The server returns an opaque bearer token. Only the SHA-256 token digest is persisted.

Sessions are fenced by the user's authentication generation as well as explicit revocation and expiry. Deactivating or reactivating a user advances the generation and revokes all existing sessions, so an old token cannot become valid again. Roles have different business authority:

- `field_operator` collects samples, transfers custody, reports field incidents, and completes assigned remediation actions.
- `lab_analyst` receives custody, records results, and submits results for independent review.
- `protection_supervisor` registers protected assets, publishes plans, reviews results, controls permits, claims incident command, and approves remediation.

Login failures and lock windows are persisted. Legacy SHA-256 password records can be upgraded to bcrypt in the same transaction that clears failures and creates the session.

## Business Workflows

### Protected source and station registration

A source belongs to one organization and has an IANA business timezone. Protection zones belong to the same source and organization. A station can only be registered when both its source and zone are active and the zone belongs to that source. Registration and its durable audit event commit together.

### Sampling and custody

A supervisor schedules an active station and an eligible field operator inside a bounded sampling window. Published plans accept collection only from the assigned operator or a supervisor. Collection performs these operations atomically:

1. Revalidates the published plan, actor, time window, and bottle requirement.
2. Allocates the next station/business-day sequence under a transaction and version condition.
3. Creates the sample label and collected sample.
4. Appends the first custody event.
5. Completes the sampling plan.
6. Writes the audit event.

Custody then moves from `collected` to `in_transit` and `received`; each transition checks the current custodian and optimistic version and appends an immutable event in the same transaction. Samples cannot skip directly to tested or archived.

### Laboratory review and exceedance response

Only a received sample can receive a laboratory result. The result progresses from draft to submitted and must be reviewed by a different analyst. Approving a result marks the sample tested. If its value exceeds the regulatory limit, the same transaction also creates a linked pollution incident, an outbox notification, and an audit event. A failure in any step rolls back all of them.

### Permit and discharge control

Permits have explicit validity windows and draft, active, suspended, expired, and revoked states. Activation is blocked while the same source has an unresolved confirmed laboratory exceedance. Discharge reports require an active permit at the event time and enforce the daily volume limit inside the transaction.

An idempotency key is scoped to organization and permit. Repeating the same report returns the persisted event without double counting; using the key for a different volume or event time returns a conflict.

### Incident command and remediation

Pollution incidents progress through reported, assessing, contained, remediating, and resolved phases. A supervisor claims command with a random lease token, generation, and expiry. All command mutations verify the user, token, generation, and live lease, preventing an expired commander from writing after another claim.

Containment resources and assignees are persisted with their own lifecycle. Resolution is rejected until containment assignments and remediation actions are complete. Remediation plan creation and all initial actions are atomic; duplicate action keys roll back the plan. Approval and action completion use optimistic versions, while batch-oriented callers can preserve successful independent actions and report item-specific failures.

### Telemetry and workers

Telemetry ingestion is idempotent per organization, station, and external reading ID. An above-threshold reading creates one alert job in the same transaction. Workers claim jobs with an owner, random token, generation, and expiry. Completion and retry updates require the same fencing fields.

The alert processor creates the corresponding incident, notification outbox event, and audit record in one transaction. Outbox delivery follows the same lease pattern and acknowledges only after delivery returns successfully. Failures persist a bounded error, exponential backoff, and eventually a permanent dead state. Cancellation propagates from the process context into claims, processors, notification delivery, and SQL calls.

## Database

The embedded versioned migration builds the schema from an empty database. Startup serializes migration work with an immediate SQLite transaction, records completed versions, and safely repeats on an existing database. Foreign keys are enabled on every connection; WAL and a busy timeout support bounded concurrent access.

The schema includes organizations, users, sessions, water sources, protection zones, monitoring stations, sampling plans, sample sequences, samples, custody events, laboratory results, permits, discharge events, incidents, containment assignments, remediation plans, remediation actions, telemetry readings, alert jobs, audit events, outbox events, and scoped idempotency records. Foreign keys, check constraints, unique constraints, status columns, versions, time fields, and claim indexes enforce persistence invariants.

Tests use temporary real SQLite database files. They cover empty migration, repeated migration, foreign keys, commit and rollback, close/reopen recovery, session fencing, optimistic updates, sequence rollback, worker lease fencing, retry/dead states, and complete cross-service workflows.

## HTTP Contract

Request and response bodies are JSON. Unknown input fields and bodies over 1 MiB are rejected. Protected endpoints require `Authorization: Bearer <token>`.

Every response includes `X-Request-ID`. A valid incoming request ID is preserved; otherwise the server creates one. Error bodies have a stable shape:

```json
{
  "error": {
    "code": "conflict",
    "message": "resource state conflicts with the operation",
    "request_id": "f5f12d9099c73418220ce044"
  }
}
```

The API distinguishes validation, authentication, authorization, missing resources, conflicts and invalid transitions, capacity limits, deadlines, and internal dependency failures. Internal error chains are logged with the request ID but are not returned to clients.

Current HTTP entry points include:

```text
POST /v1/auth/login
POST /v1/auth/logout
GET  /v1/sources
POST /v1/sources
POST /v1/sampling/plans
POST /v1/samples/collect
POST /v1/incidents
POST /v1/incidents/{id}/claim
POST /v1/telemetry/readings
GET  /healthz
GET  /readyz
```

Service packages expose the additional zone, station, custody, laboratory, permit, containment, and remediation operations to HTTP adapters without coupling business logic to transport types.

## Verification

Run the native quality gates from the repository root:

```bash
GOTOOLCHAIN=local go test ./... -count=1
GOTOOLCHAIN=local go test -race ./... -count=1
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build ./...
```

Build and run the container:

```bash
docker build -t waterpro:local .
docker run --rm -p 8080:8080 waterpro:local
```

The image uses Go 1.22.5 for a reproducible static build and a non-root distroless runtime. `/data` is owned by the non-root user and stores the SQLite database by default.
