# Project: async-commit-hook

## Goal
Run trusted repository checks asynchronously against committed source, with durable receipts and exact-commit validation shared by humans and coding agents.

## Project ID
`async-commit-hook`; the only installed executable is `ach`.

## Domain Ownership Map
- `cmds/async-commit-hook`: Go core, CLI, workers, local API, stdio MCP, installation and update lifecycle.
- `apps/async-commit-hook`: React/Rsbuild UI embedded in ach.
- `apps/async-commit-hook-docs`: Rspress documentation and installer endpoints at https://ach.delino.io.
- `protos/async_commit_hook/v1`: versioned Connect protocol.
- `packages/async-commit-hook-api-client`: generated TypeScript/Connect Query client.
- `packaging/async-commit-hook`: supported release metadata and compatibility evidence.

## Domain Contract Documents
- [Command contract](cmds-async-commit-hook-contract.md)
- [Application contract](apps-async-commit-hook-contract.md)
- [Public documentation contract](apps-async-commit-hook-docs-foundation.md)
- [Protocol contract](protos-async-commit-hook-v1-contract.md)
- [Client contract](packages-async-commit-hook-api-client-contract.md)
- [Requirements snapshot](cmds-async-commit-hook-requirements.md)
- [Release and recovery contract](cmds-async-commit-hook-release-contract.md)

## Cross-Domain Invariants
Issue #897 is normative, with explicit owner amendments on 2026-09-19: actual six-target machine validation is excluded from this implementation, and public releases/deployments are prepared but not executed. Cross-compilation and local automated integration remain required. Never label an untested platform as integration-validated.

Configuration, local storage, CLI JSON and API contracts start at version 1. Product IDs are UUID v7; commits retain Git object IDs. A successful commit or query is not successful validation. All clients use the same exact-commit, compatible-context, latest-accepted-attempt gate. Reading never acknowledges a run. Retention never resurrects an older success.

Receipt acceptance remains distinct from runner startup across CLI, MCP and Connect. The browser retains accepted rerun IDs on startup failure and offers direct navigation instead of treating the request as unaccepted.

Defaults: daemon mode, API loopback port 46309, frontend development port 46308, project configuration `.config/async-commit-hook.toml`, personal configuration `~/.config/async-commit-hook/config.toml`, state `~/.local/share/async-commit-hook`, indefinite retention. Port conflicts fail. Root `pnpm dev` remains DevHud-owned.

Results, reports and credentials remain local: this deliberately overrides the repository's R2 file-storage default. Trusted host commands are not a hostile-code sandbox. No telemetry, remote result storage, login autostart, automatic update, execution timeout or global command concurrency cap.

Release preparation is available through `Release Project` with project `async-commit-hook`: it synchronizes all seven source/installer version fields and creates the exact version tag. Signing, GitHub Release, Homebrew and Pages remain separate manual operations through the existing release workflow; see the release contract for exact-commit validation and recovery boundaries.

Primary public installation instructions follow the downloaded installers' synchronized versions, and upgrade instructions select the highest published stable release. Explicit version selection and rollback remain documented separately without pinning the primary commands to a historical release.

## Change Policy
Update the owning domain contract, the relevant requirements or release contract, and relevant AGENTS.md alongside interface, ownership, security or lifecycle changes. Generate protocol sources; never edit generated output. Public documentation describes supported user workflows, not repository internals.

## References
- https://github.com/delinoio/oss/issues/897
- [Repository defaults](repository-defaults.md)
- [Project template](project-template.md)

Run-list transport carries total check counts without repeating check arrays across a page; detail navigation retrieves complete checks. The generated client preserves optional count presence for older-response compatibility.

Repository discovery uses bounded worktree pages across the Connect, generated client and sidebar boundary. Stable IDs join split repository pages; display truncation never changes trusted filesystem paths or historical identity.

Branch navigation uses bounded streaming pages with a worktree-scoped cursor shared by the command, protocol, client and app contracts.

Branch navigation preserves opaque worktree-scoped identities independently of normalized display labels across source browsing and retained execution history.

Changes preserve safe server diagnostics when automatic diff-base resolution has no result: `diff-base-required` remains local-base guidance rather than being classified as a disconnected UI.

The owner amendment on 2026-09-20 replaces remote-hosted UI/pairing with an embedded same-origin local UI served by the daemon or on-demand viewer, and moves public docs/installers to a dedicated Rspress app. Check execution, evidence and gate semantics remain unchanged. Public deployment remains a separate manual release operation. Documentation development/preview use fixed ports 46311/46281.

