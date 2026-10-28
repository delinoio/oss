# TaiRouter supporting files

- Follow root instructions and [TaiRouter ownership](../../docs/project-tairouter.md).
- Read the [operations contract](../../docs/servers-tairouter-operations-contract.md) before changing deployment or validation.
- Keep fixtures separate from live provider, production and native OS acceptance.

- Local evaluation uses `pnpm dev:tairouter`; lifecycle, configuration and state preservation follow the [environment contract](../../docs/repository-environment-contract.md#local-development). Validate launcher changes with `node --test scripts/tairouter/local.test.mjs` and the owning Compose checks.
