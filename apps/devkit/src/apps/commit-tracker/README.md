# Commit Tracker Mini App

This mini app provides a Phase 1 operational dashboard for commit-level engineering metrics.

## Route Contract
- `/apps/commit-tracker`

## Phase 1 Features
- Metric series query with provider/repository/branch/environment filtering.
- Pull request base-vs-head metric comparison with evaluation verdicts.
- GitHub report publish action (comment + commit status) through backend API.

## API Proxy Routes
- `GET /api/commit-tracker/series`
- `GET /api/commit-tracker/comparison`
- `POST /api/commit-tracker/report`

## References
- `docs/project-devkit-commit-tracker.md`
- `docs/project-devkit.md`
