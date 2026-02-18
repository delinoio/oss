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

## Documentation Lifecycle Rules
- Every structural repository change must update relevant `docs/project-*.md` files in the same change set.
- New project creation is blocked until its project document exists.
- Domain-level `AGENTS.md` files are policy mirrors and must stay aligned with `docs/`.
- If a project splits into multiple deployables, the project doc must include path ownership and integration boundaries.
- `docs/project-devkit-commit-tracker.md` remains the canonical single document for commit tracker UI/API/collector contracts.
