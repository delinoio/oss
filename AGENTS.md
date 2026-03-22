### Instructions

- Use the `@docs/` directory as the source of truth for project contracts and implementation documents.
- All repository-wide rules must be defined in the appropriate AGENTS.md.
- List files in `docs/` before starting each task, and keep `docs/` up-to-date.
- After completing each task, update the relevant `AGENTS.md` and `docs/` files in the same change when policies, structure, or contracts changed.
- Write all code and comments in English.
- When introducing a workaround, leave sufficient comments that explain why it exists, its scope, and the conditions for removing it.
- Prefer enum types over strings whenever possible.
- If you modified Rust code, run `cargo test` from the root directory before finishing your task.
- If you modified frontend code, run `pnpm test` from the frontend directory before finishing your task.
- Commit your work as frequent as possible using git. Do NOT use `--no-verify` flag.
- Run `git commit` only after `git add`; once files are staged, commit without unnecessary delay so staged changes are preserved in history.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- After addressing pull request review comments and pushing updates, mark the corresponding review threads as resolved.
- When no explicit scope is specified and you are currently working within a pull request scope, interpret instructions within the current pull request scope.
- Do not guess; rather search for the web.
- Debug by logging. You should write enough logging code.
- Write sufficient logs for debugging and operational troubleshooting.
- Prefer structured logging libraries for business and system logs (Go: `log/slog`, Rust: `tracing`).
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.
- Prefer React Query for frontend server-state management when it is available.
- When using React Query with Connect RPC, use `@connectrpc/connect-query` from `https://github.com/connectrpc/connect-query-es`.
- When accessing `github.com`, use the GitHub CLI (`gh`) instead of browser-based workflows when possible.
- Run GitHub CLI (`gh`) commands outside sandbox restrictions by default; use the required approval flow when escalation is needed.
- When writing shell commands or scripts, treat backticks and command substitution carefully, prefer `$(...)` over legacy backticks, and apply strict escaping for all dynamic values.
- If an operation is blocked by sandbox restrictions, retry it without sandbox restrictions using the required approval flow.

### Monorepo Structure Map

- `docs/`: Source of truth for project contracts and repository documentation.
- `apps/`: User-facing apps (Next.js and React Native).
- `crates/`: Rust crates and Rust-based tooling.
- `protos/`: Shared Connect RPC proto contracts used by multi-runtime projects.
- `cmds/`: Go command tools for workflow orchestration.
- `servers/`: Backend services and APIs.
- `packaging/`: Package-manager template assets for release automation.
- `.agents/skills/`: Workspace-local Codex skills and reusable agent workflows.

### Canonical Directory Map

- `docs/README.md`: Canonical docs catalog and naming rules.
- `docs/project-template.md`: Required structure for `project-<id>` index docs.
- `docs/domain-template.md`: Required structure for domain-level contract docs.
- `docs/project-<id>.md`: Canonical project index docs (ownership + domain-doc index + cross-domain invariants).
- `docs/<domain>-<project-or-component>-<contract>.md`: Canonical domain contract docs (`apps`, `cmds`, `servers`, `crates`, `protos`, `packages`).
- `docs/project-cargo-mono.md`: Cargo subcommand project index.
- `docs/project-nodeup.md`: Node.js version manager project index.
- `docs/project-derun.md`: Derun CLI project index.
- `docs/project-ttl.md`: TTL compiler project index.
- `docs/project-mpapp.md`: Expo mobile app project index.
- `docs/project-devkit.md`: Devkit host platform project index.
- `docs/project-devkit-commit-tracker.md`: Commit Tracker scaffold project index.
- `docs/project-devkit-remote-file-picker.md`: Remote File Picker mini app project index.
- `docs/project-thenv.md`: Thenv multi-component project index.
- `docs/project-public-docs.md`: Public docs app project index.
- `docs/project-serde-feather.md`: Serde Feather multi-crate project index.
- `docs/project-dexdex.md`: DexDex multi-runtime project index.
- `docs/apps-dexdex-desktop-app-foundation.md`: DexDex app runtime and integration foundation contract.
- `docs/apps-dexdex-ui-contract.md`: DexDex UI and interaction contract.
- `docs/apps-dexdex-user-guide-contract.md`: DexDex end-user workflow contract.
- `docs/apps-dexdex-notification-contract.md`: DexDex notification UX and delivery contract.
- `docs/apps-dexdex-workspace-connectivity-contract.md`: DexDex workspace connectivity model contract.
- `docs/servers-dexdex-main-server-foundation.md`: DexDex main-server control-plane contract.
- `docs/servers-dexdex-worker-server-foundation.md`: DexDex worker-server execution contract.
- `docs/servers-dexdex-event-streaming-contract.md`: DexDex workspace event-stream contract.
- `docs/servers-dexdex-pr-management-contract.md`: DexDex PR polling and remediation contract.
- `docs/protos-dexdex-v1-contract.md`: DexDex v1 proto summary contract.
- `docs/protos-dexdex-api-contract.md`: DexDex Connect RPC API contract.
- `docs/protos-dexdex-entities-contract.md`: DexDex entity and enum contract.
- `docs/protos-dexdex-plan-mode-contract.md`: DexDex plan-mode decision contract.
- `docs/cmds-ttl-language-contract.md`: TTL language syntax/type/invalidation/code-generation contract.
- `protos/dexdex/v1/dexdex.proto`: Shared DexDex Connect RPC service and enum/message contracts (`dexdex.v1`).
- `.agents/skills/gh-pr-codex-review-loop`: Skill for iteratively applying PR feedback until Codex leaves a `:+1:` reaction, with Node.js helpers for approval checks and feedback aggregation (default actor set includes `chatgpt-codex-connector[bot]`).

### Project Identifier Contract

Treat project IDs as stable enum-style values:

```ts
enum ProjectId {
  CargoMono = "cargo-mono",
  Nodeup = "nodeup",
  Derun = "derun",
  Ttl = "ttl",
  Mpapp = "mpapp",
  Devkit = "devkit",
  DevkitCommitTracker = "devkit-commit-tracker",
  DevkitRemoteFilePicker = "devkit-remote-file-picker",
  Thenv = "thenv",
  SerdeFeather = "serde-feather",
  PublicDocs = "public-docs",
  DexDex = "dexdex",
}
```

### Project Domain Ownership

- `nodeup` -> `crates/nodeup`
- `cargo-mono` -> `crates/cargo-mono`
- `derun` -> `cmds/derun`
- `ttl` -> `cmds/ttlc`
- `mpapp` -> `apps/mpapp`
- `devkit` -> `apps/devkit`
- `devkit-commit-tracker` -> `apps/devkit/src/apps/commit-tracker`
- `devkit-remote-file-picker` -> `apps/devkit/src/apps/remote-file-picker`
- `thenv` -> `cmds/thenv`, `servers/thenv`, `apps/devkit/src/apps/thenv`
- `serde-feather` -> `crates/serde-feather`, `crates/serde-feather-macros`
- `public-docs` -> `apps/public-docs`
- `dexdex` -> `servers/dexdex-main-server`, `servers/dexdex-worker-server`, `apps/dexdex`, `protos/dexdex`

### TTL Command Contract

- `cmds/ttlc` command identifiers are `build`, `check`, `explain`, and `run`.
- `ttlc run` requires `--task` and accepts optional `--args <json>` with default `{}`.
- `ttlc run` response payload includes `result`, `run_trace`, and root-task `cache_analysis`.

### Devkit Mini-App Identifier Contract

```ts
enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
  RemoteFilePicker = "remote-file-picker",
  Thenv = "thenv",
}
```

### Commit Tracker Component Contract

`devkit-commit-tracker` is currently scaffold-only in the Devkit app, while backend and collector components remain reserved for future reactivation:

```ts
enum CommitTrackerComponent {
  WebApp = "web-app",
  ApiServer = "api-server",
  Collector = "collector",
}
```

Component mapping:
- `WebApp` -> `apps/devkit/src/apps/commit-tracker`
- `ApiServer` -> planned (no active canonical path)
- `Collector` -> planned (no active canonical path)

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

### Serde Feather Component Contract

`serde-feather` is a two-component project with fixed mapping:

```ts
enum SerdeFeatherComponent {
  Core = "core",
  Macros = "macros",
}
```

- `Core` -> `crates/serde-feather`
- `Macros` -> `crates/serde-feather-macros`

### DexDex Component Contract

`dexdex` is a three-component project with fixed mapping:

```ts
enum DexDexComponent {
  MainServer = "main-server",
  WorkerServer = "worker-server",
  DesktopApp = "desktop-app",
}
```

- `MainServer` -> `servers/dexdex-main-server`
- `WorkerServer` -> `servers/dexdex-worker-server`
- `DesktopApp` -> `apps/dexdex`

### Documentation-First Policy

- New project creation requires `docs/project-<id>.md` and at least one `docs/<domain>-<project-or-component>-<contract>.md` before runtime implementation.
- Every structural change to project paths must update the corresponding project index and relevant domain contract docs in the same change.
- Repository and domain policy updates must be written in the appropriate `AGENTS.md` in the same change.
- Domain-level `AGENTS.md` files must remain aligned with `docs/` contracts.

### New Project Onboarding Checklist

- Reserve a unique `project-id`.
- Create project path skeleton and add `.gitkeep` if implementation is not started.
- Add `docs/project-<project-id>.md` using `docs/project-template.md`.
- Add at least one domain contract doc using `docs/domain-template.md`.
- Documentation-only phase may mark canonical paths as `planned` before creating path skeletons; create the skeleton in the same change where runtime implementation begins.
- Update root and domain `AGENTS.md` files when project ownership or contracts change.
- Ensure path and naming contracts are consistent across docs and AGENTS rules.

### Naming Rules

- Use lowercase kebab-case for project IDs and directory names unless runtime conventions require otherwise.
- Use `project-` prefix for project index docs.
- Use domain prefixes (`apps-`, `cmds-`, `servers-`, `crates-`, `protos-`, `packages-`) for domain contract docs.
- Use enum-like canonical identifiers in documents where values must remain stable.

### GitHub Issue Style Contract

- Apply this contract to all open/new GitHub issues.
- Use issue titles in the format `<domain>: <description>`.
- `<domain>` must use stable lowercase identifiers from project/domain contracts (for example: `ttl`, `nodeup`, `serde-feather`, `devkit/thenv`).
- `<description>` should be concise, specific, and start with a lowercase verb phrase when possible.
- Do not use bracket-style project prefixes like `[serde-feather]`.
- Use the following Markdown section order for issue bodies:
  - `## Summary`
  - `## Evidence`
  - `## Current Gap`
  - `## Proposed Scope`
  - `## Acceptance Criteria`
  - `## Test Scenarios`
  - `## Out of Scope`
- Optional `## Additional Notes` may be appended only when needed.

### Node Runtime Baseline

- Root `.nvmrc` is the canonical Node.js runtime selector for local development workflows.
- The current required runtime is Node.js `24` (LTS major line).
- When bumping the runtime baseline, update `.nvmrc` and relevant CI/runtime docs in the same change set.

### Frontend Design Rules

- Frontend work in `apps/` must follow Toss Design Guidelines for UX/UI decisions across web and mobile surfaces.
- If a form has a single critical input, that input must receive focus when the form is shown.
- Dialog UIs must support closing with the `Esc` key.

### Shell Command Safety Rules

- Use `$(...)` for command substitution; do not use legacy backticks in new scripts.
- Wrap all file paths in quotes by default in shell commands and scripts to prevent whitespace and glob-expansion bugs.
- Apply strict quoting and escaping for all dynamic shell values to prevent command injection and parsing bugs.
- Run GitHub CLI (`gh`) commands outside sandbox restrictions by default; use the required approval flow when escalation is needed.
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
- `node-dexdex-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter dexdex test`.
- `node-dexdex-ios-build`: runs `pnpm install --frozen-lockfile`, `pnpm --filter dexdex exec vite build`, `pnpm --filter dexdex exec tauri ios init --ci`, and `pnpm --filter dexdex exec tauri ios build --ci --debug --target aarch64-sim --config '{"build":{"beforeBuildCommand":"true"}}'` on `macos-latest`.
- `ci-result`: provides a single aggregate status that fails when any executed domain job fails or is cancelled.

DexDex desktop packaging CI baseline:
- `.github/workflows/dexdex-desktop-build.yml` runs on `workflow_dispatch` and weekly schedule.
- Matrix contract: `ubuntu-latest`, `macos-latest`, `windows-latest`.
- Build command contract: `pnpm --filter dexdex tauri:build`.

Change-scoped execution rules:
- CI jobs perform self-gating (there is no standalone `detect-changes` job).
- Go and Rust jobs use in-job path-based change detection via `dorny/paths-filter`.
- Node jobs use in-job Turbo affected detection via `pnpm dlx turbo@2.8.20 query affected --packages <workspace>`.
- Changes to `.github/workflows/CI.yml` force all `go`, `node`, and `rust` domain jobs to run.
- `workflow_dispatch` runs all domain jobs regardless of changed paths.
- When build or test commands change in project contracts, update this section and `.github/workflows/CI.yml` in the same commit.

Release automation baseline:
- `auto-publish` is defined in `.github/workflows/auto-publish.yml`.
- Trigger contract: runs on `push` to `main` and supports `workflow_dispatch`.
- Branch guard contract: publish job runs only when `github.ref == 'refs/heads/main'`.
- Publish command contract: `cargo run -p cargo-mono -- publish`.
- Required secret contract: `CARGO_REGISTRY_TOKEN`.
- `release-cargo-mono` is defined in `.github/workflows/release-cargo-mono.yml`.
- Trigger contract: runs on tag push `cargo-mono@v*` only (no `workflow_dispatch` trigger).
- Publish command contract: `cargo run -p cargo-mono -- publish`.
- Required secret contract: `CARGO_REGISTRY_TOKEN`.
- Non-trigger contract: tags matching `cargo-mono-v*` must not trigger `release-cargo-mono`.
- `release-nodeup` is defined in `.github/workflows/release-nodeup.yml`.
- Trigger contract: runs on tag push `nodeup@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS nodeup release artifacts, updates Homebrew (`nodeup`), and updates winget (`DelinoIO.Nodeup`).
- `release-derun` is defined in `.github/workflows/release-derun.yml`.
- Trigger contract: runs on tag push `derun@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS derun release artifacts, updates Homebrew (`derun`), and updates winget (`DelinoIO.Derun`).
- `release-dexdex` is defined in `.github/workflows/release-dexdex.yml`.
- Trigger contract: runs on tag push `dexdex@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed DexDex desktop + main/worker server artifacts, applies desktop signing/notarization secrets, updates Homebrew (`dexdex`, `dexdex-main-server`, `dexdex-worker-server`), and updates winget (`DelinoIO.DexDex`, `DelinoIO.DexDexMainServer`, `DelinoIO.DexDexWorkerServer`).

### Documentation Lifecycle Rules

- Every structural repository change must update relevant project index docs and domain contract docs in the same change set.
- New project creation is blocked until its project index doc and at least one domain contract doc exist.
- Documentation-only project onboarding may use `planned` paths, but runtime implementation must not begin before canonical paths are created and documented.
- Repository-wide and domain rules must be maintained in the appropriate `AGENTS.md`.
- When user-facing documentation content changes, update relevant pages in `apps/public-docs` in the same change set as needed.
- Run `git commit` only after `git add`; once files are staged, create the commit without unnecessary delay.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- After addressing pull request review comments and pushing updates, resolve the corresponding review threads.
- If a project splits into multiple deployables, the project index must include path ownership and integration boundaries, and component-level domain docs must exist.
