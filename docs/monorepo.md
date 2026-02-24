# OSS Monorepo Blueprint

## Repository Purpose
This repository is a multi-domain monorepo that manages tools with very different product profiles.
The monorepo is documentation-first: structure, ownership, and contracts must be documented before implementation scales.

## Domain Boundaries
- `apps/`: User-facing applications (web and mobile).
- `crates/`: Rust crates and Rust-based tooling.
- `cmds/`: Go command-line tools (current home for active Go CLIs).
- `servers/`: Backend services and APIs.
- `docs/`: Canonical project documentation and cross-project contracts.

## Canonical Directory Map
- `docs/project-template.md`: Required structure for new project docs.
- `docs/monorepo.md`: Monorepo-wide rules and contracts.
- `docs/project-nodeup.md`: Rust-based Node.js version manager.
- `docs/project-derun.md`: Go CLI for terminal-fidelity run execution and MCP output bridge access for AI.
- `docs/project-mpapp.md`: Expo React Native mobile app.
- `docs/project-devkit.md`: Next.js 16 web micro-app platform.
- `docs/project-devkit-commit-tracker.md`: Commit Tracker contracts (Web UI + API server + collector).
- `docs/project-devkit-remote-file-picker.md`: Remote File Picker mini app.
- `docs/project-thenv.md`: Secure `.env` sharing system (CLI + Server + Web).

## Project Identifier Contract
Treat project IDs as stable enum-style values:

```ts
enum ProjectId {
  Nodeup = "nodeup",
  Derun = "derun",
  Mpapp = "mpapp",
  Devkit = "devkit",
  DevkitCommitTracker = "devkit-commit-tracker",
  DevkitRemoteFilePicker = "devkit-remote-file-picker",
  Thenv = "thenv",
}
```

## Devkit Mini-App Identifier Contract

```ts
enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
  RemoteFilePicker = "remote-file-picker",
  Thenv = "thenv",
}
```

## Commit Tracker Component Contract
`devkit-commit-tracker` is documented as a single project with three planned components:

```ts
enum CommitTrackerComponent {
  WebApp = "web-app",
  ApiServer = "api-server",
  Collector = "collector",
}
```

Component mapping:
- `WebApp` -> `apps/devkit/src/apps/commit-tracker`
- `ApiServer` -> `servers/commit-tracker` (planned)
- `Collector` -> `cmds/commit-tracker` (planned)

## Devkit Routing Contract
All Devkit mini apps must be exposed at:

```txt
/apps/<id>
```

Examples:
- `/apps/commit-tracker`
- `/apps/remote-file-picker`
- `/apps/thenv`

## Thenv Component Contract
`thenv` is a three-component project with fixed mapping:

```ts
enum ThenvComponent {
  Cli = "cli",
  Server = "server",
  WebConsole = "web-console",
}
```

- `Cli` -> `cmds/thenv`
- `Server` -> `servers/thenv`
- `WebConsole` -> `apps/devkit/src/apps/thenv`

## New Project Onboarding Checklist
- Reserve a unique `project-id`.
- Create project path skeleton and add `.gitkeep` if implementation is not started.
- Add `docs/project-<project-id>.md` using `docs/project-template.md`.
- Update `docs/monorepo.md` canonical map and contracts if needed.
- Update the relevant domain-level `AGENTS.md`.
- Ensure path and naming contracts are consistent across docs.

## Naming Rules
- Use lowercase kebab-case for project IDs and directory names unless runtime conventions require otherwise.
- Use `project-` prefix for all project docs.
- Use enum-like canonical identifiers in documents where values must remain stable.

## Shell Command Safety Rules
- Use `$(...)` for command substitution; do not use legacy backticks in new scripts.
- Apply strict quoting and escaping for all dynamic shell values to prevent command injection and parsing bugs.

## Logging Rules
- Write sufficient logs to support debugging, incident analysis, and operational troubleshooting.
- Prefer structured logging over ad-hoc plain text logs for business and system events.
- Go code should use `log/slog` (or a compatible structured logger built on it).
- Rust code should use `tracing` (or a compatible structured logging facade).

## CI Baseline
Repository-wide CI is defined in `.github/workflows/CI.yml`.

Coverage expectations:
- `go-quality`: runs `go fmt ./...` (fails if formatting changes are applied) and `go vet ./...` on Ubuntu.
- `go-test`: runs `go test ./...` on `ubuntu-latest`, `macos-latest`, and `windows-latest`.
- `rust-fmt`: runs `cargo fmt --all --check`.
- `rust-clippy`: runs `cargo clippy --workspace --all-targets --all-features -- -D warnings`.
- `rust-test`: runs `cargo test --workspace --all-targets`.
- `node-devkit-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter devkit... test`.
- `node-devkit-build`: runs `pnpm install --frozen-lockfile` and `pnpm --filter devkit... build`.
- `node-mpapp-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter mpapp test`.
- `node-mpapp-lint`: runs `pnpm install --frozen-lockfile` and `pnpm --filter mpapp lint`.
- `ci-result`: provides a single aggregate status that fails when any executed domain job fails or is cancelled.

Change-scoped execution rules:
- CI uses path-based change detection to skip unaffected domain jobs by default.
- `workflow_dispatch` runs all domain jobs regardless of changed paths.
- When build or test commands change in project contracts, update this section and `.github/workflows/CI.yml` in the same commit.

## Documentation Lifecycle Rules
- Every structural repository change must update relevant `docs/project-*.md` files in the same change set.
- New project creation is blocked until its project document exists.
- Domain-level `AGENTS.md` files are policy mirrors and must stay aligned with `docs/`.
- After staging files with `git add`, create a commit with `git commit` without unnecessary delay.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- After addressing pull request review comments and pushing updates, resolve the corresponding review threads.
- If a project splits into multiple deployables, the project doc must include path ownership and integration boundaries.
- `docs/project-devkit-commit-tracker.md` remains the canonical single document for commit tracker UI/API/collector contracts.
