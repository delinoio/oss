# cmds-derun-foundation

## Scope
- Project/component: `derun` command runtime contract
- Canonical path: `cmds/derun`

## Runtime and Language
- Runtime: Go CLI
- Primary language: Go

## Users and Operators
- AI-agent operators running reproducible command sessions
- Engineers automating terminal-fidelity execution workflows

## Interfaces and Contracts
- Command identifiers and flags must remain stable for automation clients.
- Streaming output contract must preserve terminal ordering and ANSI behavior.
- MCP output bridge payloads must remain parseable and backward compatible.
- User-facing error messages must remain single-line and follow stable style contracts:
  - Usage/validation: `invalid arguments: <reason>; details: <k=v,...>; hint: <how to fix>`
  - Runtime: `failed to <action>: <cause>; details: <k=v,...>`
  - Parse failures: `parse <field>: <cause>; details: <k=v,...>`
  - Required field failures: `<field> is required; expected <type>; details: received_type=<type>, received_value=<summary>`
  - Details rendering contract:
    - Stable deterministic key order.
    - Values must be single-line, with escaped control characters and bounded length.
    - Include only safe diagnostics (`session_id`, `cursor`, `path`, `received_type`, `received_value`, `command_name`, `arg_count`, etc.).
    - Do not include secrets or raw sensitive payloads (full argv/env values, credentials, tokens).
- Compatibility-critical error tokens must remain present for automation consumers:
  - `session not found`
  - `parse <field>`
  - `session_id is required`
  - `cursor is required`

## Storage
- Uses transient run outputs and temporary process metadata.
- Any persisted execution traces must define retention and redaction behavior.

## Security
- Secret-bearing arguments and environment values must not be logged in plaintext.
- Process isolation and execution boundaries must remain explicit and auditable.

## Logging
- Use structured `log/slog` logs for run lifecycle events.
- Include command ID, run ID, exit state, and timeout/cancellation metadata.

## Build and Test
- Local validation: `go test ./cmds/derun/...`
- Repository baseline: `go test ./...`
- Keep behavior aligned with CI Go jobs in `.github/workflows/CI.yml`.

## Dependencies and Integrations
- Integrates with shell runtime behavior and AI-agent orchestration systems.
- Integrates with MCP output consumers expecting stable field semantics.

## Change Triggers
- Update `docs/project-derun.md` with this file when command shape or output contracts change.
- Update `cmds/AGENTS.md` and root `AGENTS.md` when policy or ownership contracts change.

## References
- `docs/project-derun.md`
- `docs/domain-template.md`
