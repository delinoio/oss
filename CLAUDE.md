# AI Agent Rules

When working in a specific directory, apply the rules from that directory and all parent directories up to the root.

## While working on `.`

*Source: `AGENTS.md`*

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
- `docs/project-devkit-commit-tracker.md`: Commit Tracker multi-component project index.
- `docs/project-devkit-remote-file-picker.md`: Remote File Picker mini app project index.
- `docs/project-thenv.md`: Thenv multi-component project index.
- `docs/project-public-docs.md`: Public docs app project index.
- `docs/project-serde-feather.md`: Serde Feather multi-crate project index.
- `docs/project-dexdex.md`: DexDex multi-runtime project index.
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
- `devkit-commit-tracker` -> `apps/devkit/src/apps/commit-tracker`, `servers/commit-tracker`, `cmds/commit-tracker`
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
- `ci-result`: provides a single aggregate status that fails when any executed domain job fails or is cancelled.

DexDex desktop packaging CI baseline:
- `.github/workflows/dexdex-desktop-build.yml` runs on `workflow_dispatch` and weekly schedule.
- Matrix contract: `ubuntu-latest`, `macos-latest`, `windows-latest`.
- Build command contract: `pnpm --filter dexdex tauri:build`.

Change-scoped execution rules:
- CI uses path-based change detection to skip unaffected domain jobs by default.
- Changes to `.github/workflows/CI.yml` force all `go`, `node`, and `rust` domain jobs to run.
- `workflow_dispatch` runs all domain jobs regardless of changed paths.
- When build or test commands change in project contracts, update this section and `.github/workflows/CI.yml` in the same commit.

Release automation baseline:
- `auto-publish` is defined in `.github/workflows/auto-publish.yml`.
- Trigger contract: runs on `push` to `main` and supports `workflow_dispatch`.
- Branch guard contract: publish job runs only when `github.ref == 'refs/heads/main'`.
- Publish command contract: `cargo run -p cargo-mono -- publish`.
- Required secret contract: `CARGO_REGISTRY_TOKEN`.
- `release-nodeup` is defined in `.github/workflows/release-nodeup.yml`.
- Trigger contract: runs on tag push `nodeup@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS nodeup release artifacts and updates Homebrew (`nodeup`).
- `release-derun` is defined in `.github/workflows/release-derun.yml`.
- Trigger contract: runs on tag push `derun@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS derun release artifacts and updates Homebrew (`derun`).
- `release-dexdex` is defined in `.github/workflows/release-dexdex.yml`.
- Trigger contract: runs on tag push `dexdex@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed DexDex desktop + main/worker server artifacts, applies desktop signing/notarization secrets, and updates Homebrew (`dexdex`, `dexdex-main-server`, `dexdex-worker-server`).

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


---

## While working on `apps`

*Source: `apps/AGENTS.md`*

### Instructions for `apps/`

- Follow root `AGENTS.md` and project-specific docs before adding or changing app code.
- Keep app-specific contracts synchronized in the project index doc (`docs/project-*.md`) and relevant app-domain contract docs (`docs/apps-*.md`) in the same change.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Follow Toss Design Guidelines for frontend UX/UI decisions across web and mobile apps.

### Scope in This Domain

- `apps/devkit`: Next.js 16 micro-app platform.
- `apps/mpapp`: Expo React Native mobile app.
- `apps/public-docs`: Mintlify public documentation app.
- `apps/dexdex`: Tauri desktop app (React + TypeScript frontend with Rust backend).

### Devkit Identifier Contract

Treat Devkit mini app IDs as stable enum-style values:

```ts
enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
  RemoteFilePicker = "remote-file-picker",
  Thenv = "thenv",
}
```

### Devkit Rules

- Mini app code must live at `apps/devkit/src/apps/<id>`.
- Mini app identifiers must be stable kebab-case values.
- Mini app routes must follow `/apps/<id>`.
- Shared shell concerns belong to Devkit platform modules, not mini app internals.
- New mini apps require a project index doc and an app-domain contract doc before implementation.

### mpapp Rules

- `mpapp` must remain Expo-based unless a documented architecture decision changes it.
- Bluetooth capabilities and permissions must be explicitly documented in `docs/apps-mpapp-foundation.md`.

### public-docs Rules

- `public-docs` must remain Mintlify-based unless a documented architecture decision changes it.
- Mintlify page IDs and navigation in `apps/public-docs/docs.json` must stay aligned with `docs/apps-public-docs-foundation.md`.
- When user-facing documentation behavior changes, update related `apps/public-docs` pages in the same change set.

### dexdex Rules

- `dexdex` app boundaries must keep business communication Connect RPC-first.
- Tauri bindings are integration/runtime adapters and must not become the primary business contract surface.
- `LOCAL` and `REMOTE` workspace modes must converge to the same post-resolution UX and business flow behavior.
- DexDex desktop contract consumption must use shared proto definitions from `protos/dexdex/v1` as the source of truth.
- Keep DexDex desktop app contracts synchronized with `docs/apps-dexdex-desktop-app-foundation.md` and `docs/project-dexdex.md`.
- Global shortcut question-handoff behavior (default binding, waiting-session routing, empty fallback) must remain aligned with DexDex app/server/proto contracts.
- Menu bar tray behavior remains status-only unless docs explicitly expand scope; status derivation must use active-workspace contract semantics.
- Session fork UX must keep parent-session immutability guarantees and remain limited to documented lifecycle actions.

### Multi-Component Contract Sync

- `devkit-commit-tracker` app changes must update `docs/apps-devkit-commit-tracker-web-app-foundation.md` and `docs/project-devkit-commit-tracker.md`.
- `thenv` web console changes must update `docs/apps-thenv-web-console-foundation.md` and `docs/project-thenv.md`.
- `dexdex` desktop app changes must update `docs/apps-dexdex-desktop-app-foundation.md` and `docs/project-dexdex.md`.

### Testing and Validation

- If frontend code changes in this domain, run `pnpm test` before finishing.
- If `apps/public-docs` changes, run `pnpm --filter public-docs test` before finishing.
- Update relevant docs in `docs/` for every behavior, structure, or interface change.


---

## While working on `cmds`

*Source: `cmds/AGENTS.md`*

### Instructions for `cmds/`

- Follow root `AGENTS.md` and command-specific docs in `docs/project-*.md` plus relevant `docs/cmds-*.md` files.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form string values.

### Scope in This Domain

- `cmds/derun`: Go tool for AI coding-agent workflow orchestration.
- `cmds/thenv`: Secure `.env` sharing CLI.
- `cmds/commit-tracker`: Commit Tracker collector component.
- `cmds/ttlc`: TTL compiler CLI for `.ttl` parsing/type-checking, Go code generation, `run` task execution, and cache-aware task execution contracts.

### Command Component Contract

- `cmds/commit-tracker` is the `Collector` component for `devkit-commit-tracker`.
- `cmds/thenv` is the `Cli` component for `thenv`.
- `cmds/ttlc` command runtime is defined in `docs/cmds-ttl-foundation.md`.
- TTL language semantics are defined in `docs/cmds-ttl-language-contract.md`.

### Go Command Rules

- Keep command boundaries explicit and documented.
- Keep configuration schemas documented and synchronized with implementation.
- Add enough structured logging for step-level debugging and failure diagnosis.
- Do not log secret values for sensitive workflows (including thenv operations).

### Integration Rules

- Keep integration boundaries with `apps/`, `servers/`, and other domains explicit in docs.
- Avoid undocumented cross-domain coupling.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Update `docs/project-derun.md` and `docs/cmds-derun-foundation.md` whenever derun command contracts change.
- Update `docs/project-thenv.md` and `docs/cmds-thenv-cli-foundation.md` whenever thenv CLI operations or trust boundaries change.
- Update `docs/project-devkit-commit-tracker.md` and `docs/cmds-devkit-commit-tracker-collector-foundation.md` whenever collector contracts change.
- Update `docs/project-ttl.md` and `docs/cmds-ttl-foundation.md` whenever TTL compiler command shape, cache backend, or runtime boundaries change.
- Update `docs/project-ttl.md` and `docs/cmds-ttl-language-contract.md` whenever TTL syntax/type/invalidation/code-generation contracts change.


---

## While working on `crates`

*Source: `crates/AGENTS.md`*

### Instructions for `crates/`

- Follow root `AGENTS.md` and each crate-specific project document.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums over free-form strings for stable internal and external contracts.

### Scope in This Domain

- `crates/cargo-mono`: Cargo-based Rust monorepo management CLI.
- `crates/nodeup`: Rust-based Node.js version manager.
- `crates/serde-feather`: Size-first serde runtime-facing core crate.
- `crates/serde-feather-macros`: Proc-macro companion crate for serde-feather.

### Rust Workspace Rules

- Add new crates as explicit workspace members in root `Cargo.toml`.
- Keep crate naming aligned with project IDs when possible.
- Document behavior contracts in project index docs and relevant crate-domain docs before large implementation changes.
- For new package scaffolding, default `publish = false` until publish contracts are explicitly approved.
- Prefer minimal default features and keep optional capabilities opt-in for size-sensitive crates.
- Keep proc-macro crates and runtime crates separated by explicit crate boundaries.

### nodeup-Specific Rules

- Preserve rustup-like shim behavior: symlink strategy plus executable-name dispatch.
- Keep channel and command identifiers stable and documented.
- Record storage and download behavior in project docs whenever changed.

### cargo-mono-Specific Rules

- Keep command identifiers stable and documented in `docs/project-cargo-mono.md` and `docs/crates-cargo-mono-foundation.md`.
- Preserve `cargo mono` subcommand compatibility (`cargo-mono` binary naming contract).
- Ensure release automation (`bump`, `publish`) logs include structured operational context.

### serde-feather-Specific Rules

- Keep `serde-feather` as the runtime-facing crate and `serde-feather-macros` as the proc-macro crate.
- Keep binary-size-first defaults: minimal default features and no convenience dependencies by default.
- Keep stable derive macro identifiers (`FeatherSerialize`, `FeatherDeserialize`) aligned with `docs/project-serde-feather.md` and crate component docs.

### Multi-Component Contract Sync

- `serde-feather` core crate changes must update `docs/crates-serde-feather-core-foundation.md` and `docs/project-serde-feather.md`.
- `serde-feather-macros` changes must update `docs/crates-serde-feather-macros-foundation.md` and `docs/project-serde-feather.md`.

### Testing and Validation

- If Rust code changes in this domain, run `cargo test` from repository root.
- Keep logs sufficient for debugging install, dispatch, and runtime resolution flow.
- Keep CLI logs colorized by default for human operators, with explicit opt-out controls.


---

## While working on `servers`

*Source: `servers/AGENTS.md`*

### Instructions for `servers/`

- Follow root `AGENTS.md`, project index docs, and relevant `docs/servers-*.md` contracts before implementation.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums or typed constants over free-form strings for API contracts.

### Scope in This Domain

- `servers/thenv`: Backend for secure environment sharing.
- `servers/commit-tracker`: Commit Tracker API server component.
- `servers/dexdex-main-server`: DexDex control-plane Go server scaffold.
- `servers/dexdex-worker-server`: DexDex execution-plane Go server scaffold.

### Server Language and Data Rules

- Servers in this domain must be implemented in Go.
- SQL queries and type-safe data access must use `sqlc`.
- Protobuf definitions should live at `proto/<service_name>/v1/*.proto` unless a project contract explicitly uses a shared cross-runtime proto root.
- DexDex server contracts use shared proto definitions at `protos/dexdex/v1/*.proto`.
- Each server project must provide a local protobuf generation script and a `go generate` entrypoint.
- Keep API boundaries explicit and versionable.
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.
- Keep authorization and audit behavior documented and testable.
- Never expose secret values in logs or default API responses.

### Fixed Server Project Structure

Stateful server projects under `servers/<service_name>/` should follow this minimum structure:

- `cmd/<service_name>/main.go`
- `internal/service/`
- `internal/contracts/`
- `internal/logging/`
- `db/query/`
- `db/migrations/`
- `db/sqlc.yaml`
- `proto/<service_name>/v1/*.proto`
- `buf.yaml`
- `buf.gen.yaml`
- `scripts/generate-go-proto.sh`
- `generate.go` (with `go:generate` directive)

Scaffold-only service projects may start with a smaller structure (`main.go` + `internal/service`) when documented in the project index and matching server-domain contract docs, but must adopt explicit contract/data/logging subdirectories before persistence and public API rollout.

### Integration Rules

- Changes to server interfaces must be synchronized with related CLI and app contracts.
- Update `docs/project-thenv.md` and `docs/servers-thenv-server-foundation.md` for every thenv interface or trust model update.
- Update `docs/project-devkit-commit-tracker.md` and `docs/servers-devkit-commit-tracker-api-server-foundation.md` for every commit-tracker API contract update.
- Update `docs/project-dexdex.md` and relevant DexDex server-domain docs for every server interface or ownership contract update.
- DexDex session-fork support decisions must be capability-driven and normalized by `main-server`/`worker-server`; unsupported fork requests must map to `FAILED_PRECONDITION`.
- DexDex worker provider-native fork payloads must remain worker-internal diagnostics and must not be exposed through public server/app contracts.
- DexDex workspace work-status aggregation semantics for tray rendering must stay synchronized with proto and desktop app contracts.

### Multi-Component Contract Sync

- `servers/commit-tracker` changes must keep collector and web contracts synchronized.
- `servers/thenv` changes must keep CLI and web-console contracts synchronized.
- `servers/dexdex-main-server` and `servers/dexdex-worker-server` changes must keep proto and desktop contracts synchronized.

### Testing and Validation

- Run relevant Go tests (`go test`) when code in this domain changes.
- Keep operational logging sufficient for incident debugging and audit reconstruction.
