# cmds-devmon-foundation

## Scope
- Project/component: `devmon` daemon/CLI contract
- Canonical path: `cmds/devmon`

## Runtime and Language
- Runtime: Go daemon CLI
- Primary language: Go

## Users and Operators
- Developers running recurring workspace automations
- Operators managing command schedules and daemon lifecycle

## Interfaces and Contracts
- Schedule definitions and command execution options must remain stable and documented.
- Daemon lifecycle controls must provide deterministic start/stop/status behavior.
- Configuration schema must remain versioned and backward compatible.

## Storage
- Persists runtime schedule state and automation metadata.
- Runtime logs and transient command outputs require explicit retention policy.

## Security
- Commands run under explicit workspace boundaries.
- Secret values in environment/config must be redacted in logs.

## Logging
- Use structured `log/slog` logs for scheduling, execution, and lifecycle state changes.
- Include schedule ID, workspace path, command hash, and execution outcome.

## Build and Test
- Local validation: `go test ./cmds/devmon/...`
- Repository baseline: `go test ./...`
- CI alignment: Go quality and test jobs in `.github/workflows/CI.yml`.

## Dependencies and Integrations
- Integrates with local shell execution runtime and workspace directories.
- May integrate with UI/menu bar controller surfaces through explicit interfaces.

## Change Triggers
- Update `docs/project-devmon.md` with this file whenever command, config, or schedule contracts change.
- Keep `cmds/AGENTS.md` and root `AGENTS.md` synchronized with ownership and policy updates.

## References
- `docs/project-devmon.md`
- `docs/domain-template.md`
