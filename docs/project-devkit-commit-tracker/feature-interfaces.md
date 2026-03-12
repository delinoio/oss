# Feature: interfaces

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
- `series`, `comparison`, and `report` proxies preserve upstream HTTP status for Commit Tracker RPC failures (for example 400/401/412) instead of collapsing all failures into 502.

