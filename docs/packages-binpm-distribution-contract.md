# binpm npm distribution

## Scope

`packages/binpm` owns the private source workspace that generates the binpm npm packages and their trusted publishing. The Rust CLI stays owned by `crates/binpm`.

The current stage reserves the package names. It publishes version `0.0.0` placeholders and configures trusted publishing. It does not publish a working `npx @delino/binpm` command yet.

## Runtime and Language

Packaging and release tooling is ESM on the repository's Node.js baseline and uses only Node.js built-ins plus the `npm` and `tar` commands. The reservation packages contain no code.

## Users and Operators

Release maintainers. The end goal is that JavaScript developers can run `npx @delino/binpm <args>`. The binary installs on Node-free hosts stay with the existing direct installers, Homebrew, and `cargo binstall`.

## Interfaces and Contracts

- The launcher package is `@delino/binpm`. It will expose the `binpm` command, so `npx @delino/binpm` runs it. The unscoped `binpm` name is unavailable: npm rejects it as too similar to the existing `bin_pm` package. A plain `npx binpm` needs npm support to grant the name and is not part of this contract.
- Native packages are `@delino/binpm-<suffix>` with these suffixes, one for each first-party binpm release target: `darwin-x64`, `darwin-arm64`, `win32-x64-msvc`, `win32-arm64-msvc`, `linux-x64-gnu`, and `linux-arm64-gnu`. Linux packages declare `libc: ["glibc"]`. binpm publishes no musl binary, so no musl package exists.
- The suffix list in `packages/binpm/scripts/common.mjs` must stay aligned with the target matrix in `release-binpm.yml`.
- Placeholder packages carry only `package.json`, `README.md`, and the Apache-2.0 `LICENSE`. They have no `bin`, lifecycle script, or dependency. Packaging rejects every version except `0.0.0`.
- Generated manifests use only the first-party repository and the `https://oss.delino.io/binpm` homepage.
- Replacing the placeholders with the launcher and native packages needs a separate change. That change must add the exact-version `optionalDependencies`, byte-for-byte integrity comparison of existing versions, and a source-version link to `crates/binpm`. It must also decide how `Release Project` and `release-binpm.yml` feed the npm workflow.

## Storage

Generated tarballs and `artifacts.json` live under the ignored `packages/binpm/dist`. Never track them. Remove repository-owned `dist` directories after local verification.

## Security

- Every package must use a Trusted Publisher for repository `delinoio/oss` and workflow file `release-binpm-npm.yml`. Do not rename the file or publish from another workflow without updating all seven npm configurations.
- The workflow uses no npm token and no `NODE_AUTH_TOKEN`. Only the `publish` job gets `id-token: write`, and it runs on `main` in `delinoio/oss` after the package job succeeds.
- Publication needs no repository variable. It runs only for a manual dispatch with `dry_run: false`, on `main` in `delinoio/oss`. Dry runs, the default, never publish and never receive an OIDC token.
- The workflow installs npm `11.6.2` before publishing. The OIDC minimum is `11.5.1`.
- `npm trust` requires an existing package, so trusted publishing cannot publish the first version of a new package name. A maintainer publishes `0.0.0` once with `publish.mjs --bootstrap`, using their own npm login with two-factor authentication. This path is refused in GitHub Actions and publishes without provenance. It runs `npm publish` with the maintainer's terminal attached, because npm can ask for the two-factor approval only on a TTY.
- Existing versions are never overwritten or unpublished.

## Logging

Packaging and publication print one JSON event for each action. The events contain the package, version, and integrity, and never tokens or environment values.

## Build and Test

- `pnpm --filter @delino/binpm-source test` runs the packaging, verification, and publication fixtures. The release-contract CI job also runs them, with the workflow-shape test in `scripts/ci/binpm-npm-release.test.mjs`.
- `node packages/binpm/scripts/package.mjs` writes and verifies the seven tarballs. `node packages/binpm/scripts/publish.mjs` is a dry run.
- These checks do not contact the npm registry for writes and do not show that npm accepts the trusted publisher configuration. npm does not validate that configuration when it is saved, so a mistake appears only at publication.

## Dependencies and Integrations

Integrates with npm and GitHub Actions OIDC. `Release Project` does not publish to npm for binpm in this stage. The `binpm@v*` tag workflow is unchanged.

### One-time name reservation

A maintainer with publish rights to the `@delino` scope does these steps once, from a checkout of the merged workflow:

1. Run `node packages/binpm/scripts/package.mjs`.
2. Run `node packages/binpm/scripts/publish.mjs --bootstrap` while logged in to npm. It publishes the six native packages and then `binpm`.
3. Run `node packages/binpm/scripts/publish.mjs --print-trust-commands`. Run each printed `npm trust github ...` command with npm `11.15.0` or later.

## Change Triggers

Update this contract, `project-binpm.md`, the workflow, the CI path rules, and the packages `AGENTS.md` together when package names, the target list, the publisher identity, or the release flow change.

## References

- [Project index](project-binpm.md)
- [binpm Rust foundation](crates-binpm-foundation.md)
- [Repository workflow](repository-workflow-contract.md)
- [clibox npm distribution](packages-clibox-distribution-contract.md)
- [npm trust](https://docs.npmjs.com/cli/v11/commands/npm-trust/)
