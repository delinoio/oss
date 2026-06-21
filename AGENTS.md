### Instructions

- Use the `@docs/` directory as the source of truth for project contracts and implementation documents.
- All repository-wide rules must be defined in the appropriate AGENTS.md.
- List files in `docs/` before starting each task, and keep `docs/` up-to-date.
- After completing each task, update the relevant `AGENTS.md` and `docs/` files in the same change when policies, structure, or contracts changed.
- For documentation authoring and editing tasks, do not arbitrarily omit, delete, or simplify requested or source-backed content; if content, scope, or intent is ambiguous, ask the user before deciding what to remove, merge, or reinterpret; if the documentation change affects repository or domain policy boundaries, update or create the relevant `AGENTS.md` file in the same change when needed.
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
- `apps/`: User-facing apps (React Native and documentation web surfaces).
- `crates/`: Rust crates and Rust-based tooling.
- `protos/`: Shared Connect RPC proto contracts used by multi-runtime projects.
- `cmds/`: Go command tools for workflow orchestration.
- `servers/`: Backend services and APIs.
- `packaging/`: Package-manager template assets for release automation.
- `.agents/skills/`: Workspace-local Codex skills and reusable agent workflows.

### Canonical Directory Map

- `docs/README.md`: Canonical docs catalog and naming rules.
- `docs/repository-defaults.md`: Repository-wide default technology choices.
- `docs/project-template.md`: Required structure for `project-<id>` index docs.
- `docs/domain-template.md`: Required structure for domain-level contract docs.
- `docs/project-<id>.md`: Canonical project index docs (ownership + domain-doc index + cross-domain invariants).
- `docs/<domain>-<project-or-component>-<contract>.md`: Canonical domain contract docs (`apps`, `cmds`, `servers`, `crates`, `protos`, `packages`).
- `docs/project-binpm.md`: binpm binary package manager project index.
- `docs/apps-binpm-docs-foundation.md`: binpm Rspress documentation app, route, validation, canonical production URL, and Cloudflare Pages deployment contract.
- `docs/project-cargo-mono.md`: Cargo subcommand project index.
- `docs/project-nodeup.md`: Node.js version manager project index.
- `docs/project-with-watch.md`: Command rerun watcher CLI project index.
- `docs/project-derun.md`: Derun CLI project index.
- `docs/project-ttl.md`: TTL compiler project index.
- `docs/project-mpapp.md`: Expo mobile app project index.
- `docs/project-thenv.md`: Thenv multi-component project index.
- `docs/project-public-docs.md`: Public docs app project index.
- `docs/project-serde-feather.md`: Serde Feather multi-crate project index.
- `docs/project-rustia.md`: Rustia multi-crate project index.
- `docs/crates-binpm-foundation.md`: binpm Rust CLI, release asset source selection, global cache, and local tooling contract.
- `docs/crates-with-watch-foundation.md`: with-watch CLI and watcher foundation contract.
- `docs/crates-rustia-core-foundation.md`: Rustia core runtime LLM data contract.
- `docs/crates-rustia-llm-foundation.md`: Rustia aisdk tool adapter contract.
- `docs/crates-rustia-macros-foundation.md`: Rustia macros derive contract.
- `docs/cmds-ttl-language-contract.md`: TTL language syntax/type/invalidation/code-generation contract.
- `docs/apps-nodeup-docs-foundation.md`: Nodeup Rspress documentation app, route, validation, and Cloudflare Pages deployment contract.
### Project Identifier Contract

Treat project IDs as stable enum-style values:

```ts
enum ProjectId {
  Binpm = "binpm",
  CargoMono = "cargo-mono",
  Nodeup = "nodeup",
  WithWatch = "with-watch",
  Derun = "derun",
  Ttl = "ttl",
  Mpapp = "mpapp",
  Thenv = "thenv",
  SerdeFeather = "serde-feather",
  Rustia = "rustia",
  PublicDocs = "public-docs",
}
```

### Project Domain Ownership

- `nodeup` -> `crates/nodeup`, `apps/nodeup-docs`
- `binpm` -> `crates/binpm`, `apps/binpm-docs`
- `with-watch` -> `crates/with-watch`
- `cargo-mono` -> `crates/cargo-mono`
- `derun` -> `cmds/derun`
- `ttl` -> `cmds/ttlc`
- `mpapp` -> `apps/mpapp`
- `thenv` -> `cmds/thenv`, `servers/thenv`
- `serde-feather` -> `crates/serde-feather`, `crates/serde-feather-macros`
- `rustia` -> `crates/rustia`, `crates/rustia-llm`, `crates/rustia-macros`
- `public-docs` -> `apps/public-docs`

### Repository Default Technology Choices

- Follow `docs/repository-defaults.md` when a more specific project or domain contract does not choose a different approach.
- New persisted entities should use UUID v7 identifiers by default unless a documented compatibility, storage, protocol, or product issue requires another ID shape.
- AI-based search should default to Cloudflare AI Search unless a project contract documents a different backend and migration boundary.
- When a new project does not specify its primary language, default to Golang.
- Prefer Rspack-family build tools when possible, including Rsbuild and Rspress for app and documentation surfaces.
- Static sites under `apps/` should use Rsbuild/Rspress-style toolchains and deploy to Cloudflare Pages by default. Existing documented exceptions remain valid until their project contract changes.
- File handling should default to Cloudflare R2 object storage plus signed URLs for upload and download access unless a project contract documents another storage or access pattern.

### TTL Command Contract

- `cmds/ttlc` command identifiers are `build`, `check`, `explain`, and `run`.
- `ttlc run` requires `--task` and accepts optional `--args <json>` with default `{}`.
- `ttlc run` response payload includes `result`, `run_trace`, and root-task `cache_analysis`.

### binpm Cache Contract

- `~/.binpm/cache` is the user-level global asset cache shared by all `binpm` installs for the same account.
- `binpm` cache reuse must be validated with the strongest available integrity source: provider asset digest, upstream checksum material, successfully verified signature, or locally recorded SHA-256 metadata.
- Cache management and diagnostic command identifiers are `list`, `prune`, `clean`, and `key` under `binpm cache`.
- `binpm cache prune` and `binpm cache clean` must not remove installed package records or executable links/copies under `~/.binpm/bin`.
- `binpm cache key` must be read-only and must not download, install, or populate cache entries.

### binpm Source Contract

- Stable `binpm` source identifiers are `github:owner/repo[@version]`, `github:<host>/owner/repo[@version]`, and `gitlab:<host>/<namespace...>/<project>[@version]`.
- GitLab versionless installs must exclude upcoming releases, releases with future `released_at` values, and prerelease tag patterns.
- GitLab release asset links must use HTTPS link URLs and HTTPS final redirect targets before candidate scoring or download.
- GitLab generated `assets.sources` source archives must not be selected as installable assets.
- Direct URLs, registries, and package-manager backends remain out of scope until documented in `docs/crates-binpm-foundation.md`.

### binpm Local Tooling Contract

- `binpm.toml` is the committed project-local tool declaration file.
- `binpm.lock` is the committed deterministic project-local resolution file and must keep target-specific records.
- `binpm init` manifest creation must target the current Git worktree root when available, otherwise the nearest ancestor containing `binpm.toml` when present, otherwise the current directory.
- `binpm.lock` must not include install timestamps, last-used timestamps, absolute cache paths, or other machine-local operational metadata.
- `binpm.lock` must store sanitized canonical asset URLs only, never query strings, fragments, credential-bearing URLs, or expiring signed download URLs.
- Project-local executable files must be installed under `$repoRoot/.binpm/bin`.
- Local `binpm remove` must clean project-local package records when they exist.
- Local target-specific asset overrides must use `[tools.<cmd>.targets.<target-key>]` in `binpm.toml`.
- Local `binpm install`, `binpm update`, and `binpm x` must honor `--frozen-lockfile`; `CI=true` enables frozen behavior by default, and `--no-frozen-lockfile` is the explicit escape hatch.
- `binpm verify --require-verified` must fail when no provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified signature under a documented trust policy is available.
- `--no-confirm` is a stable scripting flag for bypassing confirmation prompts on future dangerous operations.

### binpm Docs App Contract

- `apps/binpm-docs` is the Rspress static documentation app for `binpm` and uses the existing `apps/*` workspace.
- The canonical production URL for `apps/binpm-docs` is `https://binpm.delino.io`.
- `apps/binpm-docs` must use Cloudflare Pages as the default static deployment target unless `docs/project-binpm.md` and `docs/apps-binpm-docs-foundation.md` document a replacement.
- binpm documentation content must be sourced from repository contracts and must not infer product behavior or page content from the live `https://binpm.delino.io` site.

### Thenv Component Contract

`thenv` is a two-component project with fixed mapping:

```ts
enum ThenvComponent {
  Cli = "cli",
  Server = "server",
}
```

- `Cli` -> `cmds/thenv`
- `Server` -> `servers/thenv`

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

### Rustia Component Contract

`rustia` is a three-component project with fixed mapping:

```ts
enum RustiaComponent {
  Core = "core",
  Llm = "llm",
  Macros = "macros",
}
```

- `Core` -> `crates/rustia`
- `Llm` -> `crates/rustia-llm`
- `Macros` -> `crates/rustia-macros`

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
- Documentation-only phase may mark canonical paths as `planned` before creating path skeletons; create the skeleton and add explicit workspace membership in the same change where Rust runtime implementation begins.
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
- `<domain>` must use stable lowercase identifiers from project/domain contracts (for example: `ttl`, `nodeup`, `serde-feather`, `thenv`).
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
- `node-mpapp-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter mpapp test`.
- `node-mpapp-lint`: runs `pnpm install --frozen-lockfile` and `pnpm --filter mpapp lint`.
- `node-nodeup-docs-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter nodeup-docs test`.
- `node-public-docs-test`: runs `pnpm install --frozen-lockfile` and `pnpm --filter public-docs test`.
- `ci-result`: provides a single aggregate status that fails when any executed domain job fails or is cancelled.

Change-scoped execution rules:
- CI jobs perform self-gating (there is no standalone `detect-changes` job).
- Go and Rust jobs use in-job path-based change detection via `dorny/paths-filter`.
- Node jobs use in-job Turbo affected detection via `pnpm dlx turbo@2.9.14 query affected --packages <workspace>`.
- Changes to `.github/workflows/CI.yml` force all `go`, `node`, and `rust` domain jobs to run.
- `workflow_dispatch` runs all domain jobs regardless of changed paths.
- When build or test commands change in project contracts, update this section and `.github/workflows/CI.yml` in the same commit.

Release automation baseline:
- `auto-publish` is defined in `.github/workflows/auto-publish.yml`.
- Trigger contract: runs on `push` to `main` and supports `workflow_dispatch`.
- Branch guard contract: publish job runs only when `github.ref == 'refs/heads/main'`.
- Publish command contract: `cargo run -p cargo-mono -- publish`.
- Workflow permission contract: `permissions.contents: write`.
- Tag push contract: after successful publish command execution, run `git push --tags` without no-tag fallback handling.
- Tag push authentication contract: checkout must disable persisted credentials (`persist-credentials: false`) and clear `http.https://github.com/.extraheader` before pushing tags so `GH_TOKEN` auth is authoritative.
- Required secret contract: `CARGO_REGISTRY_TOKEN` (crate publish) and `GH_TOKEN` (tag push authentication and Homebrew release workflow PR submissions). `GH_TOKEN` must be a dedicated non-`GITHUB_TOKEN` credential so tag pushes emit downstream `push` events for tag-triggered workflows.
- `release-cargo-mono` is defined in `.github/workflows/release-cargo-mono.yml`.
- Trigger contract: runs on tag push `cargo-mono@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS cargo-mono release artifacts to GitHub Releases for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`.
- `release-nodeup` is defined in `.github/workflows/release-nodeup.yml`.
- Trigger contract: runs on tag push `nodeup@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS nodeup release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`nodeup-<os>-<arch>[.exe]`) and archive assets (`nodeup-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`nodeup`) from prebuilt archives for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- `release-derun` is defined in `.github/workflows/release-derun.yml`.
- Trigger contract: runs on tag push `derun@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS derun release artifacts and updates Homebrew (`derun`) from GitHub release prebuilt archives (`darwin-amd64`, `darwin-arm64`, `linux-amd64`).
- `release-with-watch` is defined in `.github/workflows/release-with-watch.yml`.
- Trigger contract: runs on tag push `with-watch@v*` and supports `workflow_dispatch` (`version`, `dry_run`).
- Distribution contract: publishes signed multi-OS with-watch release artifacts for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`, including standalone prebuilt binaries (`with-watch-<os>-<arch>[.exe]`) and archive assets (`with-watch-<os>-<arch>.tar.gz|zip`), then updates Homebrew (`with-watch`) from GitHub release prebuilt archives (`darwin-amd64`, `darwin-arm64`, `linux-amd64`, `linux-arm64`).

### Documentation Lifecycle Rules

- Every structural repository change must update relevant project index docs and domain contract docs in the same change set.
- New project creation is blocked until its project index doc and at least one domain contract doc exist.
- Documentation-only project onboarding may use `planned` paths, but runtime implementation must not begin before canonical paths are created and documented.
- Repository-wide and domain rules must be maintained in the appropriate `AGENTS.md`.
- Documentation policy updates and documentation changes that introduce or modify repository/domain policy guidance must update the relevant `AGENTS.md` files in the same change, and documentation edits must not silently omit or reinterpret ambiguous requested or source-backed content without user confirmation.
- When user-facing documentation content changes, update relevant pages in `apps/public-docs` in the same change set as needed.
- Run `git commit` only after `git add`; once files are staged, create the commit without unnecessary delay.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- After addressing pull request review comments and pushing updates, resolve the corresponding review threads.
- If a project splits into multiple deployables, the project index must include path ownership and integration boundaries, and component-level domain docs must exist.
