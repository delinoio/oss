# Feature: collector-cli

## Collector CLI Contract
CLI commands:
- `commit-tracker ingest --input <path> --server <url> --token <token> [--subject <subject>]`
- `commit-tracker report [--provider github] [--repository <owner/repo>] [--pull-request <number>] [--base-commit <sha>] [--head-commit <sha>] [--environment <env>] [--metric-key <key> ...] [--fail-on never|warn|fail] [--github-output <path>] --server <url> --token <token> [--subject <subject>]`

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

Report context resolution behavior:
- `repository`: `--repository` then `GITHUB_REPOSITORY`
- `head_commit`: `--head-commit` then `GITHUB_EVENT_PATH` (`pull_request.head.sha`) then `GITHUB_SHA`
- `pull_request`: `--pull-request` then `GITHUB_EVENT_PATH` (`pull_request.number`)
- `base_commit`: `--base-commit` then `GITHUB_EVENT_PATH` (`pull_request.base.sha`)
- `environment`: defaults to `ci` unless `--environment` overrides.

Report output contract:
- `stdout` JSON keys:
  - `provider`
  - `repository`
  - `pullRequest`
  - `baseCommitSha`
  - `headCommitSha`
  - `aggregateEvaluation`
  - `commentUrl`
  - `statusUrl`
- GitHub Actions output file keys (`--github-output` then `GITHUB_OUTPUT`):
  - `aggregate_evaluation`
  - `comment_url`
  - `status_url`
  - `pull_request`
  - `base_commit_sha`
  - `head_commit_sha`
  - values are written as raw literals (no percent-encoding rewrite); multiline values use GitHub output delimiter syntax.

Report exit code behavior:
- `2`: argument parsing or input validation failures
- `1`: RPC/network failure, output-file write failure, or `--fail-on` threshold breach
- `0`: successful publish without threshold breach
- `--fail-on` default is `fail` (`FAIL` only); `warn` fails on `WARN|FAIL`; `never` never fails on evaluation result.

