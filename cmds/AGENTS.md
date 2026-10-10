# cmds working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

- Follow root `AGENTS.md` and command-specific docs in `docs/project-*.md` plus relevant `docs/cmds-*.md` files.

- Keep repository and domain rules in the appropriate `AGENTS.md` files.

- Write all source and comments in English.

- Prefer enums or typed constants over free-form string values.

- Keep command boundaries explicit and documented.

- Keep configuration schemas documented and synchronized with implementation.

- Add enough structured logging for step-level debugging and failure diagnosis.

- Do not log secret values for sensitive workflows.

- Keep integration boundaries with `apps/`, `servers/`, and other domains explicit in docs.

- Avoid undocumented cross-domain coupling.

- Run relevant Go tests (`go test`) when code in this domain changes.

## Owning contracts

- [async-commit-hook command contract](../docs/cmds-async-commit-hook-contract.md)
- [DeliDev command, server, and Worker contract](../docs/cmds-delidev-contract.md)
- [cmds-derun-foundation](../docs/cmds-derun-foundation.md)
- [Runmoor command foundation](../docs/cmds-runmoor-foundation.md)
- [Project: derun](../docs/project-derun.md)
- [Linux Package Repository Contract](../docs/repository-linux-packages-contract.md)
