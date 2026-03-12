# Feature: environment

## Environment Variables
Environment template files:
- Server: `servers/commit-tracker/.env.example`
- Devkit proxy: `apps/devkit/.env.example`

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
- `GITHUB_REPOSITORY` (optional default for `report --repository`)
- `GITHUB_SHA` (optional fallback default for `report --head-commit` after event payload resolution)
- `GITHUB_EVENT_PATH` (optional default source for `report --pull-request` and `report --base-commit`)
- `GITHUB_EVENT_PATH` (optional default source for `report --head-commit` via `pull_request.head.sha`)
- `GITHUB_OUTPUT` (optional default destination for report output key-value entries)

CLI auth resolution behavior:
- `--token` and `--subject` flags do not embed secret-bearing environment defaults in flag usage output.
- Runtime resolution order for token: `--token` then `COMMIT_TRACKER_TOKEN`.
- Runtime resolution order for subject: `--subject` then `COMMIT_TRACKER_SUBJECT` then resolved token.
- Runtime resolution order for report output file path: `--github-output` then `GITHUB_OUTPUT`.

