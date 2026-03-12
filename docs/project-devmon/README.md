# Project: devmon

## Documentation Layout
- Canonical entrypoint for this project: docs/project-devmon/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`devmon` is a Go automation daemon that runs recurring folder jobs and can now be managed as a macOS user daemon from a menu bar app.
Its primary goal is to provide safe local command scheduling with clear operational visibility and simple lifecycle control.


## Path
- Canonical CLI path: `cmds/devmon`
- Operator README: `cmds/devmon/README.md`
- Service lifecycle manager: `cmds/devmon/internal/servicecontrol`
- Menu bar app: `cmds/devmon/internal/menubar`
- Runtime status store: `cmds/devmon/internal/state`


## Runtime and Language
- Go CLI + macOS menu bar integration


## Users
- Engineers maintaining multiple local repositories or workspace folders
- Operators who need deterministic recurring local command automation
- macOS users who want to manage daemon lifecycle from the top menu bar


## In Scope
- Foreground daemon command (`devmon daemon`) with graceful shutdown behavior
- Configuration validation command (`devmon validate`)
- Service lifecycle commands (`devmon service install|uninstall|start|stop|status`) on macOS
- Menu bar command (`devmon menubar`) on macOS
- LaunchAgent installation for both daemon and menu bar processes
- TOML-based folder and job configuration (`devmon.toml`)
- Interval-based scheduling with startup run support
- Global concurrency limiting for all jobs
- Per-job overlap protection (skip when previous run is still active)
- Shell-command job execution (`shell -c script`) with timeout enforcement
- Structured logs for lifecycle, skip reasons, and command output streams
- Structured daemon status persistence for operational UI and status queries


## Out of Scope
- System-domain (`root`) service management
- Built-in non-shell job types in v1
- Remote orchestration or multi-host execution
- Queueing backlog execution when concurrency slots are exhausted
- Cross-platform menu bar implementations in this phase


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
