# Commit Tracker Mini App

This mini app provides a Phase 1 operational dashboard for commit-level engineering metrics.

## Route Contract
- `/apps/commit-tracker`

## Phase 1 Features
- Metric series query with provider/repository/branch/environment filtering.
- Pull request base-vs-head metric comparison with evaluation verdicts.
- GitHub report publish action (comment + commit status) through backend API.

## UI Presentation Contract
- The dashboard uses Devkit shared UI primitives (`dk-card`, `dk-fieldset`, `dk-form-grid`, `dk-table`, `dk-alert`, `dk-success`).
- Verdict rendering uses dedicated `dk-ct-*` badge classes:
  - `dk-ct-badge-pass`
  - `dk-ct-badge-warn`
  - `dk-ct-badge-fail`
  - `dk-ct-badge-neutral`
- Comparison rows apply lightweight verdict tints (`dk-ct-row-*`) while keeping badge-first emphasis.
- Mobile behavior keeps forms single-column on narrow screens and preserves table readability via horizontal scroll wrappers.

## API Proxy Routes
- `GET /api/commit-tracker/series`
- `GET /api/commit-tracker/comparison`
- `POST /api/commit-tracker/report`

## References
- `docs/project-devkit-commit-tracker.md`
- `docs/project-devkit.md`
