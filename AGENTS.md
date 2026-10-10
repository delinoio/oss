# Repository working instructions

## Instruction and contract ownership

- Ordinary feature additions, bug fixes, tests and behavior-contract changes do not by themselves require an AGENTS.md update. Update instructions only when development procedures, directory ownership or repository/domain development rules change. Keep feature requirements in the owning contract; do not duplicate them in AGENTS.md.
- Do not add issue-specific feature paragraphs to instruction files. Read the owning project index and domain contracts before implementation.
- Record new implementation status, validation results and unresolved limits in pull requests, issues and CI logs/artifacts. Include the source revision, commands, results and unperformed checks; distinguish fixtures, builds and packaging from actual native/account/platform acceptance. Exclude secrets, user state and raw native content. Preserve existing validation records.

## Instructions

- Use the `@docs/` directory as the source of truth for project contracts and implementation documents.
- License repository-owned source and future distributions under Apache-2.0. Keep imported code and bundled fonts under their original licenses with notices intact; follow `docs/repository-license-contract.md`.
- All repository-wide rules must be defined in the appropriate AGENTS.md.
- Every repository-owned directory named `dist` is ignored generated output and must never be tracked. Generate required `dist` content explicitly before compilation, testing, or packaging, and remove generated `dist` directories from the final worktree.
- Track repository asset files of at least 512 KiB through exact-path Git LFS attributes. Checkouts that compile, test, or package those assets must hydrate LFS objects first; preserve source licenses and notices. Schema-only Buf baseline clones must skip LFS smudging within the breaking-check command so pointer-only checkouts need no unrelated asset downloads. Follow `docs/repository-workflow-contract.md`.
- List files in `docs/` before starting each task, and keep `docs/` up-to-date.
- For documentation authoring and editing tasks, do not arbitrarily omit, delete, or simplify requested or source-backed content; if content, scope, or intent is ambiguous, ask the user before deciding what to remove, merge, or reinterpret; update or create the relevant `AGENTS.md` in the same change only when development procedures, directory ownership or repository/domain development rules change.
- Public documentation surfaces must not document repository-internal implementation details. Keep internal source-of-truth contracts, architecture notes, repo-local paths, and operational internals in `docs/`; curate public docs under `apps/*-docs` and `apps/public-docs` around user-facing behavior, supported workflows, stable public interfaces, and maintainer-facing paths only when those paths are explicitly part of the public contract.
- Write all code and comments in English.
- Write GitHub issue titles and explanatory body text in English. Preserve exact code, commands, paths, diagnostic messages, search queries, direct quotations and localized UI strings when their original text is required; explain non-English evidence in English. Translate existing non-English issue prose without changing requirements, evidence, references, attachments or issue state.
- When introducing a workaround, leave sufficient comments that explain why it exists, its scope, and the conditions for removing it.
- Prefer enum types over strings whenever possible.
- If you modified Rust code, run `cargo test` from the root directory before finishing your task.
- If you modified frontend code, run `pnpm test` from the frontend directory before finishing your task.
- Commit your work as frequent as possible using git. Do NOT use `--no-verify` flag.
- Run `git commit` only after `git add`; once files are staged, commit without unnecessary delay so staged changes are preserved in history.
- Committing may require workspace binaries (for example, git hooks). If required binaries are missing, run `pnpm install` at the repository root and retry the commit.
- Root `pnpm dev` is the DevHud team workflow. Documentation development uses `pnpm dev:public-docs` on the consolidated fixed port documented in `docs/apps-public-docs-foundation.md`; it fails on conflicts instead of automatically remapping a server.
- Keep `.infisical.json`, `.dev-environment/`, real `.env` files, user credentials, and production/release/signing credentials uncommitted. Service-local `.env.example` files contain names, validation guidance, placeholders, and safe loopback defaults only; do not add a root environment example or expose internal secret paths in public product docs.
- After addressing pull request review comments and pushing updates, mark the corresponding review threads as resolved.
- When no explicit scope is specified and you are currently working within a pull request scope, interpret instructions within the current pull request scope.
- Do not guess; rather search for the web.
- Follow `docs/repository-dependency-security-contract.md` for dependency security updates, validation evidence, and unresolved upstream constraints. Keep unresolved advisories visible; a fixed runtime pin or an unavailable patch is not a vulnerability fix.
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

## Monorepo Structure Map

- `docs/`: Source of truth for project contracts and repository documentation.
- `apps/`: User-facing apps and documentation web surfaces.
- `crates/`: Rust crates and Rust-based tooling.
- `cmds/`: Go command tools for workflow orchestration.
- `servers/`: Backend services, including the implemented DevHud API foundation.
- `protos/`: Versioned protocol schemas, including the implemented DevHud Connect RPC schemas and committed Go bindings.
- `packages/`: Shared generated and runtime packages, including the implemented DevHud API client.
- `packaging/`: Package-manager template assets for release automation.
- `.agents/skills/`: Workspace-local Codex skills and reusable agent workflows.

## Canonical Directory Map

- `docs/README.md`: Canonical docs catalog and naming rules.
- `docs/repository-defaults.md`: Repository-wide default technology choices.
- `docs/repository-environment-contract.md`: Repository configuration classification, local development modes, secret ownership, and orchestration contract.
- `docs/project-template.md`: Required structure for `project-<id>` index docs.
- `docs/domain-template.md`: Required structure for domain-level contract docs.
- `docs/project-<id>.md`: Canonical project index docs (ownership + domain-doc index + cross-domain invariants).
- `docs/<domain>-<project-or-component>-<contract>.md`: Canonical domain contract docs (`apps`, `cmds`, `servers`, `crates`, `protos`, `packages`).
- `docs/repository-<topic>-contract.md`: Canonical repository-level contract docs for cross-project workflow and policy contracts.
- `docs/project-binpm.md`: binpm binary package manager project index.
- `docs/apps-binpm-docs-foundation.md`: binpm Rspress documentation app, route, validation, canonical production URL, and Cloudflare Pages deployment contract.
- `docs/project-cargo-mono.md`: Cargo subcommand project index.
- `docs/project-clibox.md`: clibox Rust CLI, npm distribution, and public documentation project index.
- `docs/apps-clibox-docs-foundation.md`: clibox public guides and route/validation contract.
- `docs/project-pnport.md`: pnport project index, staged availability and incomplete acceptance boundary.
- `docs/apps-pnport-docs-foundation.md`: pnport public guide routes and availability contract.
- `docs/project-nodeup.md`: Node.js version manager project index.
- `docs/project-with-watch.md`: Command rerun watcher CLI project index.
- `docs/project-derun.md`: Derun CLI project index.
- `docs/project-public-docs.md`: Public docs app project index.
- `docs/apps-react-forge-docs-foundation.md`: React Forge public guide routes, content, availability, and validation contract.
- `docs/packages-docs-site-switcher-contract.md`: Shared accessible documentation site selector package contract.
- `docs/project-serde-feather.md`: Serde Feather multi-crate project index.
- `docs/project-rustia.md`: Rustia multi-crate project index.
- `docs/project-devhud.md`: DevHud cross-platform desktop/mobile utility project index and current the feature contract.
- `docs/crates-binpm-foundation.md`: binpm Rust CLI, release asset source selection, global cache, and local tooling contract.
- `docs/crates-with-watch-foundation.md`: with-watch CLI and watcher foundation contract.
- `docs/crates-rustia-core-foundation.md`: Rustia core runtime LLM data contract.
- `docs/crates-rustia-llm-foundation.md`: Rustia aisdk tool adapter contract.
- `docs/crates-rustia-macros-foundation.md`: Rustia macros derive contract.
- `docs/apps-nodeup-docs-foundation.md`: Nodeup Rspress documentation app, route, validation, and Cloudflare Pages deployment contract.
- `docs/apps-runmoor-docs-foundation.md`: Runmoor Rspress documentation app, route migration, fixed-port validation, and Cloudflare Pages deployment contract.

## Repository Default Technology Choices

- Follow `docs/repository-defaults.md` when a more specific project or domain contract does not choose a different approach.
- New persisted entities should use UUID v7 identifiers by default unless a documented compatibility, storage, protocol, or product issue requires another ID shape.
- AI-based search should default to Cloudflare AI Search unless a project contract documents a different backend and migration boundary.
- When a new project does not specify its primary language, default to Golang.
- Prefer Rspack-family build tools when possible, including Rsbuild and Rspress for app and documentation surfaces.
- Static sites under `apps/` should use Rsbuild/Rspress-style toolchains and deploy to Cloudflare Pages by default. Existing documented exceptions remain valid until their project contract changes.
- File handling should default to Cloudflare R2 object storage plus signed URLs for upload and download access unless a project contract documents another storage or access pattern.

## Documentation-First Policy

- New project creation requires `docs/project-<id>.md` and at least one `docs/<domain>-<project-or-component>-<contract>.md` before runtime implementation.
- Every structural change to project paths must update the corresponding project index and relevant domain contract docs in the same change.
- Development-procedure, directory-ownership and repository/domain development-rule updates must be written in the appropriate `AGENTS.md` in the same change.
- Domain-level `AGENTS.md` files must route to their owning contracts and follow the instruction-update policy.

## New Project Onboarding Checklist

- Reserve a unique `project-id`.
- Create project path skeleton and add `.gitkeep` if implementation is not started.
- Add `docs/project-<project-id>.md` using `docs/project-template.md`.
- Add at least one domain contract doc using `docs/domain-template.md`.
- Documentation-only phase may mark canonical paths as `planned` before creating path skeletons; create the skeleton and add explicit workspace membership in the same change where Rust runtime implementation begins.
- Update root and domain `AGENTS.md` files when directory ownership or development rules change.
- Ensure path and naming contracts are consistent across docs and AGENTS rules.

## Naming Rules

- Use lowercase kebab-case for project IDs and directory names unless runtime conventions require otherwise.
- Use `project-` prefix for project index docs.
- Use domain prefixes (`apps-`, `cmds-`, `servers-`, `crates-`, `protos-`, `packages-`) for domain contract docs.
- Use `repository-<topic>-contract.md` for repository-level contract docs that span project or domain ownership.
- Use enum-like canonical identifiers in documents where values must remain stable.

## GitHub Issue Style Contract

- Apply this contract to all open/new GitHub issues.
- Use issue titles in the format `<domain>: <description>`.
- `<domain>` must use stable lowercase identifiers from project/domain contracts (for example: `nodeup`, `serde-feather`).
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

## GitHub Pull Request Title Contract

- Apply this contract to newly created pull requests.
- Pull request titles must use Conventional Commit-style format with a required scope: `<type>(<scope>): <description>`.
- `<type>` must be an appropriate Conventional Commit type such as `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`, `build`, `perf`, or `revert`.
- `<scope>` must use a stable lowercase project, component, domain, or tooling identifier from repository contracts when one applies (for example: `nodeup`, `serde-feather`, `docs`, `ci`).
- Do not create unscoped pull request titles or use bracket-style project prefixes like `[serde-feather]`.

## Runtime Baselines

- Root `.nvmrc` is the canonical Node.js runtime selector for local development workflows.
- The current required runtime is Node.js `24` (LTS major line).
- When bumping the runtime baseline, update `.nvmrc` and relevant CI/runtime docs in the same change set.

## Frontend Design Rules

- Frontend work in `apps/` must follow Toss Design Guidelines for UX/UI decisions across web and mobile surfaces.
- If a form has a single critical input, that input must receive focus when the form is shown.
- Dialog UIs must support closing with the `Esc` key.

## Shell Command Safety Rules

- Use `$(...)` for command substitution; do not use legacy backticks in new scripts.
- Wrap all file paths in quotes by default in shell commands and scripts to prevent whitespace and glob-expansion bugs.
- Apply strict quoting and escaping for all dynamic shell values to prevent command injection and parsing bugs.

## Logging Rules

- Write sufficient logs to support debugging, incident analysis, and operational troubleshooting.
- Prefer structured logging over ad-hoc plain text logs for business and system events.
- Go code should use `log/slog` (or a compatible structured logger built on it).
- Rust code should use `tracing` (or a compatible structured logging facade).
- CLI and operator-facing logs should enable ANSI color by default; allow opt-out with documented flags or environment variables.

## Documentation Lifecycle Rules

- Every structural repository change must update relevant project index docs and domain contract docs in the same change set.
- New project creation is blocked until its project index doc and at least one domain contract doc exist.
- Documentation-only project onboarding may use `planned` paths, but runtime implementation must not begin before canonical paths are created and documented.
- Repository-wide and domain rules must be maintained in the appropriate `AGENTS.md`.
- Documentation policy updates and documentation changes that introduce or modify repository/domain development rules must update the relevant `AGENTS.md` files in the same change, and documentation edits must not silently omit or reinterpret ambiguous requested or source-backed content without user confirmation.
- When user-facing documentation content changes, update relevant pages in `apps/public-docs` in the same change set as needed.
- Run `git commit` only after `git add`; once files are staged, create the commit without unnecessary delay.
- After addressing pull request review comments and pushing updates, resolve the corresponding review threads.
- If a project splits into multiple deployables, the project index must include path ownership and integration boundaries, and component-level domain docs must exist.

## Contract navigation

- [Documentation catalog](docs/README.md) lists project indexes and domain contracts. Project indexes own canonical paths, stable project IDs and cross-domain invariants.
- [Repository workflow](docs/repository-workflow-contract.md) owns CI, release, allocation-validation records and validation procedures.
- [Environment ownership](docs/repository-environment-contract.md) owns development selectors, service configuration, child environments and shutdown.
- Read scoped instructions in every affected domain, including cross-domain consumers.
