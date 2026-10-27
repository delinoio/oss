# Repository environment contract

## Configuration ownership

Repository files contain public configuration, toolchain selectors, names and safe examples. Real `.env` files, `.infisical.json`, `.dev-environment/`, user credentials, and production, release and signing credentials remain uncommitted. Do not add a root environment example. Service-local `.env.example` files contain names, validation guidance, placeholders and safe loopback defaults only.

User-owned credentials stay in the owning local mechanism. They must not enter repository files, logs, process arguments or shared build cache metadata. Secrets belong to their owning service or protected release environment.

## Local development

Use `pnpm dev:public-docs` for the consolidated documentation site at `127.0.0.1:46302`. Port conflicts fail instead of selecting another port. Follow `apps-public-docs-foundation.md` for wrapper and shutdown behavior.

Use `pnpm dev:async-commit-hook` for the application frontend. Its development frontend uses fixed port `46308`; the local UI/API uses configurable `api_port`, default `46309`. No root team or OSS dependency orchestrator is required.

The shared `scripts/dev-environment/process.mjs` helper retains argv-based command invocation and bounded platform/tool environment construction for development wrappers. Spawn children with explicit argument arrays and `shell: false`. Windows pnpm shims use explicit `cmd.exe` invocation. Forward termination signals and wait for process trees to exit.

## Native CLI repository publication

The `linux-packages` GitHub Environment owns only the package signing subkey/passphrase and the two-bucket R2 object credentials listed in `repository-linux-packages-contract.md`. Its non-secret account ID and public fingerprint are Environment variables. Main and the seven exact project tag namespaces (including `clibox@v*`) may enter this boundary; pull requests and dry runs never receive these secrets. These values are unrelated to local service configuration, Infisical, Turbo development environments. Primary signing recovery material is operator-owned outside the repository and CI.

The async-commit-hook local UI/API remain on configurable api_port (default 46309), with frontend development on fixed 46308. Its public documentation is served by the consolidated `public-docs` app on fixed development port 46302; the root `pnpm dev:public-docs` command never invokes Infisical. No new environment input is required.
