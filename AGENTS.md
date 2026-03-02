### Instructions

- Use the `@docs/` directory as the source of truth for project contracts and implementation documents.
- All repository-wide rules must be defined in the appropriate AGENTS.md.
- List files in `docs/` before starting each task, and keep `docs/` up-to-date.
- After completing each task, update the relevant `AGENTS.md` and `docs/` files in the same change when policies, structure, or contracts changed.
- Write all code and comments in English.
- Prefer enum types over strings whenever possible.
- If you modified Rust code, run `cargo test` from the root directory before finishing your task.
- If you modified frontend code, run `pnpm test` from the frontend directory before finishing your task.
- Commit your work as frequent as possible using git. Do NOT use `--no-verify` flag.
- Run `git commit` only after `git add`; once files are staged, commit without unnecessary delay so staged changes are preserved in history.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- After addressing pull request review comments and pushing updates, mark the corresponding review threads as resolved.
- Do not guess; rather search for the web.
- Debug by logging. You should write enough logging code.
- Write sufficient logs for debugging and operational troubleshooting.
- Prefer structured logging libraries for business and system logs (Go: `log/slog`, Rust: `tracing`).
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.
- When accessing `github.com`, use the GitHub CLI (`gh`) instead of browser-based workflows when possible.
- When writing shell commands or scripts, treat backticks and command substitution carefully, prefer `$(...)` over legacy backticks, and apply strict escaping for all dynamic values.
- If an operation is blocked by sandbox restrictions, retry it without sandbox restrictions using the required approval flow.

### Monorepo Structure Map

- `docs/`: Source of truth for project contracts and repository documentation.
- `apps/`: User-facing apps (Next.js and React Native).
- `crates/`: Rust crates and Rust-based tooling.
- `cmds/`: Go command tools for workflow orchestration.
- `servers/`: Backend services and APIs.
- `.agents/skills/`: Workspace-local Codex skills and reusable agent workflows.

### Canonical Directory Map

- `docs/project-template.md`: Required structure for new project docs.
- `docs/project-cargo-mono.md`: Cargo subcommand for Rust monorepo management.
- `docs/project-nodeup.md`: Rust-based Node.js version manager.
- `docs/project-derun.md`: Go CLI for terminal-fidelity run execution and MCP output bridge access for AI.
- `docs/project-mpapp.md`: Expo React Native mobile app.
- `docs/project-devkit.md`: Next.js 16 web micro-app platform.
- `docs/project-devkit-commit-tracker.md`: Commit Tracker contracts (Web UI + API server + collector).
- `docs/project-devkit-remote-file-picker.md`: Remote File Picker mini app.
- `docs/project-thenv.md`: Secure `.env` sharing system (CLI + Server + Web).
- `docs/project-devmon.md`: Go automation daemon with macOS menu bar-managed lifecycle controls.
- `docs/project-public-docs.md`: Mintlify-based public documentation app.
- `.agents/skills/gh-pr-codex-review-loop`: Skill for iteratively applying PR feedback until Codex leaves a `:+1:` reaction, with Node.js helpers for approval checks and feedback aggregation (default actor set includes `chatgpt-codex-connector[bot]`).

### Project Identifier Contract

Treat project IDs as stable enum-style values:

```ts
enum ProjectId {
  CargoMono = "cargo-mono",
  Nodeup = "nodeup",
  Derun = "derun",
  Devmon = "devmon",
  Mpapp = "mpapp",
  Devkit = "devkit",
  DevkitCommitTracker = "devkit-commit-tracker",
  DevkitRemoteFilePicker = "devkit-remote-file-picker",
  Thenv = "thenv",
  PublicDocs = "public-docs",
}
```

### Project Domain Ownership

- `nodeup` -> `crates/nodeup`
- `cargo-mono` -> `crates/cargo-mono`
- `derun` -> `cmds/derun`
- `devmon` -> `cmds/devmon`
- `mpapp` -> `apps/mpapp`
- `devkit` -> `apps/devkit`
- `devkit-commit-tracker` -> `apps/devkit/src/apps/commit-tracker`, `servers/commit-tracker`, `cmds/commit-tracker`
- `devkit-remote-file-picker` -> `apps/devkit/src/apps/remote-file-picker`
- `thenv` -> `cmds/thenv`, `servers/thenv`, `apps/devkit/src/apps/thenv`
- `public-docs` -> `apps/public-docs`

### Devkit Mini-App Identifier Contract

```ts
enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
  RemoteFilePicker = "remote-file-picker",
  Thenv = "thenv",
}
```

### Commit Tracker Component Contract

`devkit-commit-tracker` is a single project with three active components:

```ts
enum CommitTrackerComponent {
  WebApp = "web-app",
  ApiServer = "api-server",
  Collector = "collector",
}
```

Component mapping:
- `WebApp` -> `apps/devkit/src/apps/commit-tracker`
- `ApiServer` -> `servers/commit-tracker`
- `Collector` -> `cmds/commit-tracker`

### Devkit Routing Contract

All Devkit mini apps must be exposed at `/apps/<id>`.

Examples:
- `/apps/commit-tracker`
- `/apps/remote-file-picker`
- `/apps/thenv`

### Thenv Component Contract

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

### Documentation-First Policy

- New project creation requires `docs/project-<id>.md` before runtime implementation.
- Every structural change to project paths must update the corresponding `docs/project-*.md` in the same change.
- Repository and domain policy updates must be written in the appropriate `AGENTS.md` in the same change.
- Domain-level `AGENTS.md` files must remain aligned with `docs/` contracts.

### New Project Onboarding Checklist

- Reserve a unique `project-id`.
- Create project path skeleton and add `.gitkeep` if implementation is not started.
- Add `docs/project-<project-id>.md` using `docs/project-template.md`.
- Update root and domain `AGENTS.md` files when project ownership or contracts change.
- Ensure path and naming contracts are consistent across docs and AGENTS rules.

### Naming Rules

- Use lowercase kebab-case for project IDs and directory names unless runtime conventions require otherwise.
- Use `project-` prefix for all project docs.
- Use enum-like canonical identifiers in documents where values must remain stable.

### Node Runtime Baseline

- Root `.nvmrc` is the canonical Node.js runtime selector for local development workflows.
- The current required runtime is Node.js `24` (LTS major line).
- When bumping the runtime baseline, update `.nvmrc` and relevant CI/runtime docs in the same change set.

### Frontend Design Rules

- Frontend work in `apps/` must follow Toss Design Guidelines for UX/UI decisions across web and mobile surfaces.

### Shell Command Safety Rules

- Use `$(...)` for command substitution; do not use legacy backticks in new scripts.
- Apply strict quoting and escaping for all dynamic shell values to prevent command injection and parsing bugs.
- If an operation is blocked by sandbox restrictions, retry it without sandbox restrictions using the required approval flow.

### Logging Rules

- Write sufficient logs to support debugging, incident analysis, and operational troubleshooting.
- Prefer structured logging over ad-hoc plain text logs for business and system events.
- Go code should use `log/slog` (or a compatible structured logger built on it).
- Rust code should use `tracing` (or a compatible structured logging facade).
- CLI and operator-facing logs should enable ANSI color by default; allow opt-out with documented flags or environment variables.

### CI Baseline

Repository-wide quality CI is defined in `.github/workflows/CI.yml`.

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
- `node-public-docs-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter public-docs test`.
- `ci-result`: provides a single aggregate status that fails when any executed domain job fails or is cancelled.

Change-scoped execution rules:
- CI uses path-based change detection to skip unaffected domain jobs by default.
- Changes to `.github/workflows/CI.yml` force all `go`, `node`, and `rust` domain jobs to run.
- `workflow_dispatch` runs all domain jobs regardless of changed paths.
- When build or test commands change in project contracts, update this section and `.github/workflows/CI.yml` in the same commit.

Release automation baseline:
- `auto-publish` is defined in `.github/workflows/auto-publish.yml`.
- Trigger contract: runs on `push` to `main` and supports `workflow_dispatch`.
- Branch guard contract: publish job runs only when `github.ref == 'refs/heads/main'`.
- Publish command contract: `cargo run -p cargo-mono -- publish --no-verify`.
- Required secret contract: `CARGO_REGISTRY_TOKEN`.

### Documentation Lifecycle Rules

- Every structural repository change must update relevant `docs/project-*.md` files in the same change set.
- New project creation is blocked until its project document exists.
- Repository-wide and domain rules must be maintained in the appropriate `AGENTS.md`.
- When user-facing documentation content changes, update relevant pages in `apps/public-docs` in the same change set as needed.
- Run `git commit` only after `git add`; once files are staged, create the commit without unnecessary delay.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- After addressing pull request review comments and pushing updates, resolve the corresponding review threads.
- If a project splits into multiple deployables, the project doc must include path ownership and integration boundaries.
- `docs/project-devkit-commit-tracker.md` remains the canonical single document for commit tracker UI/API/collector contracts.
