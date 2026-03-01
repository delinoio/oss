# Project: devkit-commit-tracker

## Goal
`devkit-commit-tracker` tracks commit-level engineering metrics, compares pull-request base/head commits, and publishes provider feedback as comments and commit statuses.

## Path
- Web app: `apps/devkit/src/apps/commit-tracker`
- Web route: `apps/devkit/src/app/apps/commit-tracker/page.tsx`
- Devkit API proxy routes: `apps/devkit/src/app/api/commit-tracker/*`
- API server and provider reporter: `servers/commit-tracker`
- CI collector and ingestion CLI: `cmds/commit-tracker`

## Runtime and Language
- Web app: Next.js 16 mini app module (TypeScript)
- API server: Go + Connect RPC + PostgreSQL
- Collector CLI: Go + Connect RPC client

## Users
- Developers tracking performance and artifact-size changes by commit
- Reviewers validating pull-request impact against base commits
- Engineering leads monitoring trend and regression risk

## In Scope
- Commit metric ingestion from CI and benchmark pipelines
- Pull-request base-vs-head comparisons with rule-based verdicts
- GitHub pull-request comment and commit-status publishing
- Commit, branch, repository, environment, metric, and time-range filters
- Connect RPC contracts for ingestion, query, and report flows

## Out of Scope
- Self-hosted provider support in v1
- Internal noise-correction or benchmark re-sampling in v1
- Replacing code-review systems or release orchestration

## Architecture
- Collector uploads commit metrics to `MetricIngestionService.UpsertCommitMetrics`.
- API server stores metric definitions and measurements in PostgreSQL.
- API server computes PR comparison results (`base` vs `head`) with metric-specific direction and thresholds.
- Provider reporter publishes markdown comparison comments and aggregate commit statuses.
- Devkit route `/apps/commit-tracker` is live and exposes:
  - metric series table view
  - pull-request comparison table view
  - report publish action
- Devkit dashboard UI for Commit Tracker is aligned to shared shell primitives (`dk-*`) with commit-tracker-scoped visual classes (`dk-ct-*`) for verdict emphasis.
- Devkit shell remains the owner of auth/session/navigation concerns.

## Interfaces
Canonical mini app identifier:

```ts
enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
}
```

Route contract:
- `/apps/commit-tracker`
- Current route state: live dashboard route rendered by Devkit shell.

UI presentation contract:
- Dashboard layout uses shared Devkit classes for cards, fieldsets, form grids, table wrappers, and status messaging.
- Verdict states are rendered with badge-first emphasis using `dk-ct-badge-pass|warn|fail|neutral`.
- Pull-request comparison table rows use lightweight contextual tinting (`dk-ct-row-*`) to support quick scanning without overpowering badge readability.
- Responsive behavior:
  - filter/comparison forms collapse to single-column on narrow screens
  - data tables remain horizontally scrollable via wrapper containers

Canonical component identifiers:

```ts
enum CommitTrackerComponent {
  WebApp = "web-app",
  ApiServer = "api-server",
  Collector = "collector",
}
```

Connect RPC proto path:
- `servers/commit-tracker/proto/committracker/v1/commit_tracker.proto`

Connect RPC services:

```proto
service MetricIngestionService {
  rpc UpsertCommitMetrics(UpsertCommitMetricsRequest) returns (UpsertCommitMetricsResponse);
}

service MetricQueryService {
  rpc ListMetricSeries(ListMetricSeriesRequest) returns (ListMetricSeriesResponse);
  rpc GetPullRequestComparison(GetPullRequestComparisonRequest) returns (GetPullRequestComparisonResponse);
}

service ProviderReportService {
  rpc PublishPullRequestReport(PublishPullRequestReportRequest) returns (PublishPullRequestReportResponse);
}
```

Primary enum contracts (proto values):

```txt
GitProviderKind:
- GIT_PROVIDER_KIND_GITHUB
- GIT_PROVIDER_KIND_GITLAB
- GIT_PROVIDER_KIND_BITBUCKET

MetricValueKind:
- METRIC_VALUE_KIND_UNIT_NUMBER
- METRIC_VALUE_KIND_RATIO
- METRIC_VALUE_KIND_DELTA_ONLY
- METRIC_VALUE_KIND_BOOLEAN_GATE
- METRIC_VALUE_KIND_HISTOGRAM
- METRIC_VALUE_KIND_PERCENTILES

MetricDirection:
- METRIC_DIRECTION_INCREASE_IS_BETTER
- METRIC_DIRECTION_DECREASE_IS_BETTER

EvaluationLevel:
- EVALUATION_LEVEL_PASS
- EVALUATION_LEVEL_WARN
- EVALUATION_LEVEL_FAIL
- EVALUATION_LEVEL_NEUTRAL
```

Provider behavior (Phase 1):
- `GIT_PROVIDER_KIND_GITHUB`: live publish behavior (comment + commit status)
- `GIT_PROVIDER_KIND_GITLAB`: contract available, publish path returns `FailedPrecondition`
- `GIT_PROVIDER_KIND_BITBUCKET`: contract available, publish path returns `FailedPrecondition`
- Provider enum validation is strict for RPC inputs; unknown enum values return `InvalidArgument`.
- For `PublishPullRequestReport`, unknown provider enum values are rejected as `InvalidArgument` before the Phase 1 integration gate is evaluated.

Devkit proxy API routes:
- `GET /api/commit-tracker/series`
- `GET /api/commit-tracker/comparison`
- `POST /api/commit-tracker/report`

Proxy error semantics:
- `comparison` proxy preserves upstream HTTP status for Commit Tracker RPC failures (for example 400/401/412) instead of collapsing all failures into 502.

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

Sensitive logging rule:
- `X-Commit-Tracker-Subject` and bearer token values remain required for authorization but must never be emitted in structured logs.

## Collector Input Contract
CLI command:
- `commit-tracker ingest --input <path> --server <url> --token <token> [--subject <subject>]`

Input JSON (`--input`) schema:

```json
{
  "provider": "github",
  "repository": "acme/repo",
  "branch": "main",
  "commitSha": "abc123",
  "runId": "run-001",
  "environment": "ci",
  "measuredAt": "2026-02-24T01:00:00Z",
  "metrics": [
    {
      "metricKey": "binary-size",
      "displayName": "Binary Size",
      "unit": "bytes",
      "valueKind": "unit-number",
      "direction": "decrease-is-better",
      "warningThresholdPercent": 5,
      "failThresholdPercent": 10,
      "value": 1234
    }
  ]
}
```

## Build and Test
Current commands:
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

## Environment Variables
Server:
- `COMMIT_TRACKER_DATABASE_URL` (required)
- `COMMIT_TRACKER_AUTH_TOKEN` (required)
- `COMMIT_TRACKER_GITHUB_TOKEN` (required for GitHub publish)
- `COMMIT_TRACKER_GITHUB_API_BASE` (optional; default `https://api.github.com`)
- `COMMIT_TRACKER_ADDR` (optional; default `127.0.0.1:8091`)

Devkit proxy:
- `COMMIT_TRACKER_SERVER_URL` or `NEXT_PUBLIC_COMMIT_TRACKER_SERVER_URL`
- `COMMIT_TRACKER_WEB_TOKEN` / `COMMIT_TRACKER_TOKEN`
- `COMMIT_TRACKER_WEB_SUBJECT` / `COMMIT_TRACKER_SUBJECT`

CLI:
- `COMMIT_TRACKER_SERVER_URL` (optional default)
- `COMMIT_TRACKER_TOKEN` (optional default)
- `COMMIT_TRACKER_SUBJECT` (optional default)

CLI auth resolution behavior:
- `--token` and `--subject` flags do not embed secret-bearing environment defaults in flag usage output.
- Runtime resolution order for token: `--token` then `COMMIT_TRACKER_TOKEN`.
- Runtime resolution order for subject: `--subject` then `COMMIT_TRACKER_SUBJECT` then resolved token.

## Roadmap
- Phase 1 (implemented): ingestion + comparison + GitHub publish + operational web dashboard
- Phase 2: richer graph UX and provider-adapter hardening
- Phase 3: advanced metric families and governance controls

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- `docs/project-devkit.md`
- [GitHub REST API: Issue comments](https://docs.github.com/en/rest/issues/comments#create-an-issue-comment)
- [GitHub REST API: Commit statuses](https://docs.github.com/en/rest/commits/statuses#create-a-commit-status)
