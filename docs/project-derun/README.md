# Project: derun

## Documentation Layout
- Canonical entrypoint for this project: docs/project-derun/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`derun` is a Go CLI that provides terminal-faithful command execution for humans and MCP-based output retrieval for AI.
Its primary contract is zero-intrusion command proxying for `derun run`, with side-channel transcript capture that can be queried later through `derun mcp`.


## Path
- Canonical CLI path: `cmds/derun`


## Runtime and Language
- Go CLI


## Users
- Engineers running interactive commands in local terminals
- AI coding agents that need replay/tail access to command output
- Tool maintainers who need deterministic local session capture and retrieval


## In Scope
- `derun run` command execution with terminal-fidelity proxy behavior
- Full stdin/TTY/ANSI passthrough without output mutation in user-visible streams
- Local transcript capture for AI retrieval as raw output bytes plus indexed chunks
- `derun mcp` stdio MCP server exposing session discovery and output read tools
- Historical replay and live tail over cursor-based MCP APIs
- Cross-platform transport abstraction with POSIX PTY and Windows ConPTY support
- Retention garbage collection with 24-hour default TTL for session artifacts


## Out of Scope
- Remote execution service or multi-host session aggregation
- AI-initiated keystroke injection into active user sessions in v1
- Secret redaction transforms that mutate captured raw output bytes
- Replacing shell/runtime behavior beyond proxy and capture boundaries


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
