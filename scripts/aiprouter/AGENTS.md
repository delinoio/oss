# AIPRouter supporting files

- Follow root instructions and [AIPRouter ownership](../../docs/project-aiprouter.md).
- Read the [operations contract](../../docs/servers-aiprouter-operations-contract.md) before changing deployment or validation.
- Keep fixtures separate from live provider, production and native OS acceptance.

- Local evaluation uses `pnpm dev:aiprouter`; lifecycle, configuration and state preservation follow the [environment contract](../../docs/repository-environment-contract.md#local-development). Validate launcher changes with `node --test scripts/aiprouter/local.test.mjs` and the owning Compose checks.
