# Project: devkit-commit-tracker

## Goal
`devkit-commit-tracker` tracks commit-level engineering metrics and highlights regressions.
It provides commit history visualization, pull-request base-vs-head comparisons, and provider feedback via comments and status checks.

## Path
- Web app: `apps/devkit/src/apps/commit-tracker`
- Web route placeholder: `apps/devkit/src/app/apps/commit-tracker/page.tsx`
- API server and provider reporter: `servers/commit-tracker` (planned)
- CI collector and ingestion CLI: `cmds/commit-tracker` (planned)

## Runtime and Language
- Web app: Next.js 16 mini app module (TypeScript)
- API server: Go (planned)
- Collector CLI: Go (planned)

## Users
- Developers tracking performance and artifact-size changes by commit
- Reviewers validating pull request impact against base commits
- Engineering leads monitoring trend and regression risk

## In Scope
- Commit metric ingestion from CI and benchmark pipelines
- Multi-provider pull request comparison reporting for cloud providers:
  GitHub, GitLab, Bitbucket
- Graph-based visualization for metric trends and distribution views
- Metric-specific evaluation rules for increase/decrease semantics
- Pull request comment publishing and status check publishing
- Commit, branch, repository, and environment filters for metric exploration
- Connect RPC contracts for ingestion, query, and reporting flows

## Out of Scope
- Self-hosted provider support in v1
- Built-in statistical noise-correction algorithms for benchmark data
- Replacement of code review systems or approval workflows
- Full release orchestration and deployment automation

## Architecture
- Collector uploads commit metrics from CI and benchmark tools through Connect RPC ingestion endpoints.
- API server stores metric definitions and measurements, then computes commit and pull request comparisons (`base` vs `head`).
- Provider reporter emits markdown comparison comments and aggregate status checks to provider APIs.
- Web app route is currently a Devkit shell placeholder with enum-based registration.
- Full visualization UI (timelines, deltas, percentile/histogram summaries) is deferred after shell bootstrap.
- Devkit shell continues to own global auth/session/navigation concerns.

## Interfaces
Canonical mini app identifier:

```ts
enum MiniAppId {
  CommitTracker = "commit-tracker",
}
```

Route contract:
- `/apps/commit-tracker`
- Current route state: placeholder page rendered by Devkit shell bootstrap.

Canonical component identifiers:

```ts
enum CommitTrackerComponent {
  WebApp = "web-app",
  ApiServer = "api-server",
  Collector = "collector",
}
```

Provider and metric contract identifiers:

```ts
enum GitProviderKind {
  GitHub = "github",
  GitLab = "gitlab",
  Bitbucket = "bitbucket",
}

enum MetricValueKind {
  UnitNumber = "unit-number",
  Ratio = "ratio",
  DeltaOnly = "delta-only",
  BooleanGate = "boolean-gate",
  Histogram = "histogram",
  Percentiles = "percentiles",
}

enum MetricDirection {
  IncreaseIsBetter = "increase-is-better",
  DecreaseIsBetter = "decrease-is-better",
}

enum EvaluationLevel {
  Pass = "pass",
  Warn = "warn",
  Fail = "fail",
  Neutral = "neutral",
}
```

Connect RPC service contract:

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

Pull request reporting contract:
- Publish markdown table comment containing:
  metric key, `base`, `head`, absolute delta, delta percent, evaluation level.
- Publish aggregate status check for pull request head commit:
  `pass`, `warn`, or `fail`.
- Use metric-specific rule profile with:
  `warning_threshold_percent`, `fail_threshold_percent`, `direction`.

Graph and visualization contract:
- Commit timeline line chart per metric.
- Pull request delta table/bar view (`base`, `head`, `delta`, `delta%`, `verdict`).
- Histogram and percentile visualization for latency/memory distributions.
- Color mapping is driven by metric direction and thresholds, not fixed increase/decrease assumptions.
- Required filter dimensions: provider, repository, branch, metric, time range, environment.

v1 metric catalog contract:
- Binary size (`bytes`, `UnitNumber`)
- Benchmark duration (`ms`, `UnitNumber`)
- Peak memory (`bytes`, `UnitNumber`)
- Throughput (`ops/s`, `UnitNumber`)
- CPU time (`ms`, `UnitNumber`)
- Error rate (`%`, `Ratio`)
- Success rate (`%`, `Ratio`)
- Test/benchmark gate (`pass/fail`, `BooleanGate`)
- Latency percentiles (`p50/p95/p99`, `Percentiles`)
- Histogram summary from external benchmark tooling (`Histogram`)

## Storage
- Web app stores local filter/view preferences.
- API server owns persistent comparison data.
- Collector does not require long-lived local storage by default.

Core entity contracts:
- `MetricDefinition`:
  metric key, display name, unit, value kind, direction, thresholds.
- `CommitMeasurement`:
  provider, repository, branch, commit SHA, run ID, environment, metric values.
- `PullRequestComparison`:
  provider pull request reference, base/head SHAs, per-metric comparisons, aggregate verdict.
- `RuleProfile`:
  metric-specific `direction`, `warning_threshold_percent`, `fail_threshold_percent`.

Noise policy contract:
- v1 stores and compares values produced by external benchmark tools.
- No internal noise-correction or re-sampling algorithm in v1.

## Security
- Respect Devkit auth/session controls in the web app.
- Use provider tokens with minimum scopes required for comment and status APIs.
- Avoid storing provider secrets in frontend contexts.
- Do not expose sensitive environment metadata in default responses.

## Logging
Required baseline logs:
- Data ingestion lifecycle and failures
- Pull request comparison generation lifecycle and failures
- Provider comment/status publication attempts and outcomes
- Route/load failures and filter application errors in the web app

Required structured fields:
- `provider`
- `repository`
- `pull_request`
- `commit`
- `run_id`
- `metric_key`
- `evaluation_level`
- `delta_percent`

## Build and Test
Current commands:
- Web app tests: `pnpm --filter devkit... test`
- API server tests (planned): `go test ./servers/commit-tracker/...`
- Collector tests (planned): `go test ./cmds/commit-tracker/...`

Acceptance-focused test scenarios:
- Idempotent behavior when the same commit metrics are uploaded repeatedly.
- `Neutral` comparison behavior when base commit metric is missing.
- `IncreaseIsBetter` metrics pass on increase and degrade on decrease.
- `DecreaseIsBetter` metrics pass on decrease and degrade on increase.
- Pull request comment verdicts stay consistent with status check verdicts.
- Provider adapters stay isolated per provider integration path.
- Histogram/percentile metrics appear in both graph views and pull request comparisons.
- External benchmark outputs are stored directly without internal recalculation.

## Roadmap
- Phase 1: Contracts, ingestion flow, and base-vs-head comparison core.
- Phase 2: Graph UX expansion and provider adapter hardening.
- Phase 3: Advanced metric families and governance controls.

## Open Questions
- Long-term retention and archival policy for high-volume metric time-series.
- Access-control granularity for cross-team repository visibility.
- Optional support timeline for self-hosted provider variants.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- `docs/project-devkit.md`
- [GitHub REST API: Pull request comments](https://docs.github.com/en/rest/pulls/comments)
- [GitHub REST API: Create a commit status](https://docs.github.com/en/rest/commits/statuses#create-a-commit-status)
- [GitLab API: Notes (merge request notes)](https://docs.gitlab.com/api/notes/#create-new-merge-request-note)
- [GitLab API: Commits (set commit pipeline status)](https://docs.gitlab.com/api/commits/#set-commit-pipeline-status)
- [Bitbucket Cloud REST API: Pull requests](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-pullrequests/)
- [Bitbucket Cloud REST API: Commit statuses](https://developer.atlassian.com/cloud/bitbucket/rest/api-group-commit-statuses/)
