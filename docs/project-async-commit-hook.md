# Project: async-commit-hook

## Goal
Run trusted repository checks asynchronously against committed source, with durable receipts and exact-commit validation shared by humans and coding agents.

## Project ID
`async-commit-hook`; the only installed executable is `ach`.

## Domain Ownership Map
- `cmds/async-commit-hook`: Go core, CLI, workers, local API, stdio MCP, installation and update lifecycle.
- `apps/async-commit-hook`: React/Rsbuild static application and public `/docs`.
- `protos/async_commit_hook/v1`: versioned Connect protocol.
- `packages/async-commit-hook-api-client`: generated TypeScript/Connect Query client.
- `packaging/async-commit-hook`: supported release metadata and compatibility evidence.

## Domain Contract Documents
- [Command contract](cmds-async-commit-hook-contract.md)
- [Application contract](apps-async-commit-hook-contract.md)
- [Protocol contract](protos-async-commit-hook-v1-contract.md)
- [Client contract](packages-async-commit-hook-api-client-contract.md)
- [Requirements snapshot](cmds-async-commit-hook-requirements.md)
- [Implementation evidence](cmds-async-commit-hook-evidence.md)
- [Release and recovery contract](cmds-async-commit-hook-release-contract.md)

## Cross-Domain Invariants
Issue #897 is normative, with two explicit owner amendments on 2026-09-19: actual six-target machine validation is excluded from this implementation, and public releases/deployments are prepared but not executed. Cross-compilation and local automated integration remain required. Never label an untested platform as integration-validated.

Configuration, local storage, CLI JSON and API contracts start at version 1. Product IDs are UUID v7; commits retain Git object IDs. A successful commit or query is not successful validation. All clients use the same exact-commit, compatible-context, latest-accepted-attempt gate. Reading never acknowledges a run. Retention never resurrects an older success.

Defaults: daemon mode, API loopback port 46309, frontend development port 46308, project configuration `.config/async-commit-hook.toml`, personal configuration `~/.config/async-commit-hook/config.toml`, state `~/.local/share/async-commit-hook`, indefinite retention. Port conflicts fail. Root `pnpm dev` remains DevHud-owned.

Results, reports and credentials remain local: this deliberately overrides the repository's R2 file-storage default. Trusted host commands are not a hostile-code sandbox. No telemetry, remote result storage, login autostart, automatic update, execution timeout or global command concurrency cap.

## Change Policy
Update the owning domain contract, evidence matrix and relevant AGENTS.md alongside interface, ownership, security or lifecycle changes. Generate protocol sources; never edit generated output. Public documentation describes supported user workflows, not repository internals.

## References
- https://github.com/delinoio/oss/issues/897
- [Repository defaults](repository-defaults.md)
- [Project template](project-template.md)
