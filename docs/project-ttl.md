# Project: ttl

## Goal
Define the TTL compiler project contract for task graph validation, execution, and cache-aware runtime behavior.

## Project ID
`ttl`

## Domain Ownership Map
- `cmds/ttlc`

## Domain Contract Documents
- `docs/cmds-ttl-foundation.md`
- `docs/cmds-ttl-language-contract.md`

## Cross-Domain Invariants
- Command identifiers remain stable: `build`, `check`, `explain`, `run`.
- `ttlc run` keeps `--task` required and `--args <json>` optional with default `{}`.
- `ttlc run` result payload keeps `result`, `run_trace`, and root-task `cache_analysis` fields.

## Change Policy
- Update this index, `docs/cmds-ttl-foundation.md`, and `docs/cmds-ttl-language-contract.md` together when command or language contracts change.
- Keep command/runtime behavior aligned with `cmds/AGENTS.md` and root `AGENTS.md`.
- Keep user-facing error/diagnostic message template IDs and wording policy aligned with `cmds/ttlc/internal/messages`.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
