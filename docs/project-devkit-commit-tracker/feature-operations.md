# Feature: operations

## Storage
Primary backend storage:
- PostgreSQL via `COMMIT_TRACKER_DATABASE_URL`

Migration behavior:
- Server auto-applies schema migrations at startup using `schema_migrations`.
- Migration claiming is concurrency-safe via `INSERT ... ON CONFLICT DO NOTHING`, so parallel server startups do not fail on duplicate migration inserts.

Core tables:
- `metric_definitions`
  - `metric_key`, `display_name`, `unit`, `value_kind`, `direction`, thresholds
- `commit_measurements`
  - provider, repository, branch, commit SHA, run ID, environment, metric key, value, measured_at
  - unique key for idempotent ingest:
    `(provider, repository, branch, commit_sha, run_id, environment, metric_key)`
- `pull_request_reports`
  - provider, repository, pull_request, base/head SHAs, environment, aggregate verdict, markdown, provider URLs


## Security
Server auth contract:
- Required request header: `Authorization: Bearer <token>`
- Required request header: `X-Commit-Tracker-Subject`
- Shared token validation for CLI and Devkit proxy requests.

Provider secrets:
- GitHub publish requires `COMMIT_TRACKER_GITHUB_TOKEN`.
- Do not expose provider tokens to frontend runtime.


## Logging
Required baseline logs:
- Ingestion lifecycle success/failure
- Pull-request comparison lifecycle success/failure
- Provider publish attempts and outcomes
- Route/UI loading and publish failures in web app
- Authorization denied attempts (`result=denied`) for all RPC entrypoints

Required structured fields:
- `provider`
- `repository`
- `pull_request`
- `commit`
- `run_id`
- `metric_key`
- `evaluation_level`
- `delta_percent`

Web UI payload shape for commit-tracker route logs:
- `provider` remains a top-level log field.
- `repository`, `pull_request`, `commit`, `run_id`, `metric_key`, `evaluation_level`, and `delta_percent` are emitted in the `context` map with stable snake_case keys.

Web UI placeholder defaults (when a singular value is not applicable):
- `pull_request`: `0`
- `commit`: `""`
- `run_id`: `""`
- `metric_key`: `""`
- `evaluation_level`: `""`
- `delta_percent`: `0`

Publish failure context requirement:
- `commit-tracker-report-publish` failure logs include `pull_request` and `commit` when those values are provided by user input.

Sensitive logging rule:
- `X-Commit-Tracker-Subject` and bearer token values remain required for authorization but must never be emitted in structured logs.


## Build and Test
Current commands:
- Proto generation (server-local): `go generate ./servers/commit-tracker`
- Proto generation (workspace): `./scripts/generate-go-proto.sh`
- Web app tests: `pnpm --filter devkit... test`
- API server tests: `go test ./servers/commit-tracker/...`
- Collector CLI tests: `go test ./cmds/commit-tracker/...`
- Full Go test pass: `go test ./...`

Acceptance-focused scenarios:
- Idempotent ingest for repeated commit/run/metric uploads
- `Neutral` verdict when base metric is missing
- Direction-aware increase/decrease evaluation
- Deterministic delta-percent behavior when base value is `0`
- Deterministic latest metric snapshot selection when multiple rows share the same `measured_at` timestamp
- Unknown provider enum values return `InvalidArgument`
- GitHub publish path writes comment + status and persists report row
- GitHub auth failure maps to auth error response code
- Unsupported provider publish paths return `FailedPrecondition`
- Authorization failures return `Unauthenticated` and emit structured denied logs without token/subject leakage
- Connect handler e2e path verifies `UpsertCommitMetrics` via generated client -> handler -> service
- Report CLI resolves PR context from GitHub event payload and env defaults when flags are omitted
- Report CLI writes both stdout JSON and GitHub Actions output keys
- Report CLI applies `--fail-on` thresholds deterministically (`never|warn|fail`)

