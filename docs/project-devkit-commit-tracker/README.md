# Project: devkit-commit-tracker

## Documentation Layout
- Canonical entrypoint for this project: docs/project-devkit-commit-tracker/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`devkit-commit-tracker` tracks commit-level engineering metrics, compares pull-request base/head commits, and publishes provider feedback as comments and commit statuses.


## Path
- Web app: `apps/devkit/src/apps/commit-tracker`
- Web route: `apps/devkit/src/app/apps/commit-tracker/page.tsx`
- Devkit API proxy routes: `apps/devkit/src/app/api/commit-tracker/*`
- API server and provider reporter: `servers/commit-tracker`
- Server-local proto generation script: `servers/commit-tracker/scripts/generate-go-proto.sh`
- Generated Go RPC code (gitignored; regenerate via `go generate ./servers/commit-tracker` or `./scripts/generate-go-proto.sh`): `servers/commit-tracker/gen/proto/committracker/v1`
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


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-collector-cli.md](./feature-collector-cli.md)
- [feature-environment.md](./feature-environment.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
