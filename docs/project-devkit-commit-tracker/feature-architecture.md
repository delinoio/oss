# Feature: architecture

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

