# binpm npm distribution

## Scope

`packages/binpm` owns the private source workspace, npm launcher, native package registry, packaging, installed-consumer checks and trusted publishing. The Rust CLI stays owned by `crates/binpm`.

Version `0.0.0` remains an immutable historical name reservation without code. Subsequent stable releases contain the launcher and native binaries.

## Runtime and Language

Packaging and release tooling is ESM on the repository's Node.js 24 baseline and uses Node.js built-ins plus npm. Published launcher code is CommonJS and requires Node.js 24 or later. The binaries themselves do not require Node.js.

## Users and Operators

JavaScript developers can run `npx @delino/binpm <args>` or install `@delino/binpm` globally. Native installation through direct installers, Homebrew and `cargo binstall` remains available without Node.js. Release maintainers use automatic tag releases or credential-free manual dry runs.

## Interfaces and Contracts

- The launcher package is `@delino/binpm` and exposes the `binpm` command. The unscoped `binpm` name is unavailable: npm rejects it as too similar to the existing `bin_pm` package. A plain `npx binpm` needs npm support to grant the name and is not part of this contract.
- Native packages are `@delino/binpm-<suffix>`: `darwin-x64`, `darwin-arm64`, `win32-x64-msvc`, `win32-arm64-msvc`, `linux-x64-gnu`, and `linux-arm64-gnu`. Linux packages declare `libc: ["glibc"]`. binpm publishes no musl binary, so no musl package exists.
- `packages/binpm/src/platforms.cjs` owns the target registry. The npm build matrix and the existing `release-binpm.yml` native matrix must match its six Rust targets.
- Generated versions come directly from `crates/binpm/Cargo.toml`; the private tooling workspace version is not a product version. Versions must be stable SemVer and cannot be `0.0.0`. Every generated package records the exact source commit in `gitHead`.
- The launcher declares all six native packages as exact-version `optionalDependencies`. It selects the current OS, CPU and libc, resolves the package with Node's module resolver, checks the dependency version and executable, and starts it with literal arguments and inherited stdio/cwd/environment. It preserves numeric exit codes, forwards Unix signals, and reproduces signal termination. Windows console interrupts already reach both processes and must not be forwarded with forceful `child.kill()`.
- Unknown platforms, musl Linux, absent optional dependencies and mismatched native versions fail with installation guidance. The launcher never downloads binaries or runs lifecycle scripts. Cross-platform copied `node_modules` and installation with omitted optional dependencies are unsupported.
- Every package includes the full Apache-2.0 `LICENSE`, root `NOTICE` and package README. The closed tarball inventory, executable mode, source metadata, launcher source bytes and license/notice bytes are verified without extracting arbitrary paths. Native package creation runs the binary on its matching host and requires `--version` to match Cargo.
- Generated manifests use the first-party repository and `https://oss.delino.io/binpm` homepage.

## Storage

Generated package directories and tarballs live under ignored `packages/binpm/dist`. Never track them. Remove repository-owned `dist` directories after local verification. Complete seven-tarball artifacts are named `binpm-npm-<version>-<revision>` and retained for 30 days; intermediate native artifacts use `binpm-npm-native-<suffix>`.

## Security

- Every package uses a Trusted Publisher for repository `delinoio/oss` and workflow file `release-binpm-npm.yml`. Do not rename the file or move publication without updating all seven npm configurations.
- No npm token or `NODE_AUTH_TOKEN` is used. Build, assembly and manual dry-run jobs have read-only contents permissions. Only the publish job gets `id-token: write`.
- Tag pushes publish automatically. Live publication, including a manual retry, requires the exact `refs/tags/binpm@v<source-version>` and exact workflow source commit in the first-party repository. Publication validates the trusted workflow identity and GitHub Actions OIDC environment again in the publisher script. No repository enable variable is required.
- Manual dispatch defaults to `dry_run: true`; a development ref can build and verify without publication. `dry_run: false` requires the exact version tag. Workflow concurrency serializes npm releases without canceling prior runs.
- The publish job installs npm `11.6.2`; the trusted-publishing minimum is `11.5.1`.
- Before any write, inspect every existing registry version and require byte-for-byte `dist.integrity` equality. Publish and confirm all six native packages before the launcher. Readback polls accepted uploads without repeating the upload. Conflicting or unconfirmed integrity stops publication.
- Retry a partially published release using only the failed publish job and its retained complete tarball set. Do not rebuild already published package bytes, overwrite npm versions, move tags or unpublish historical placeholders. Missing or expired artifacts require explicit maintainer recovery.

## Logging

Packaging, consumer checks and publication print JSON events with safe target, package, version, revision and integrity fields. Never log tokens, environment values or raw process reports.

## Build and Test

- `pnpm --filter @delino/binpm-source test` runs package, target-selection, launcher, source-identity and immutable-publication fixtures. The release-contract CI job also runs them and the workflow checks in `scripts/ci/binpm-npm-release.test.mjs`.
- Build a native binary from the exact source revision, then run `node packages/binpm/scripts/package.mjs binary --target <rust-target> --binary <path>` on the matching native host.
- `node packages/binpm/scripts/smoke.mjs --directory packages/binpm/dist/tarballs` installs the native tarball and generated launcher in a temporary offline npm consumer with scripts disabled, then checks installed command help/version and a native error exit code. It does not replace all-platform native acceptance.
- Assembly downloads all six verified native packages, runs `node packages/binpm/scripts/package.mjs main`, then `node packages/binpm/scripts/package.mjs verify --output packages/binpm/dist/tarballs` and the publisher's default dry run.
- Fixtures and local packaging do not establish hosted native runner or OIDC acceptance. npm does not validate a saved trusted publisher until a real publication.

## Dependencies and Integrations

`Release Project` remains the owner of version commits and `binpm@v*` tags. That tag starts `release-binpm-npm.yml` automatically, independently of the existing GitHub Release/Homebrew/native Linux workflow. The npm workflow builds the same six targets on native hosts. GNU Linux uses the shared digest-pinned compatibility image and baseline compiler flags. Every host verifies installed npm execution before the complete-set assembly and OIDC publication.

### Trusted publisher setup

The package names were initially reserved by authenticated maintainer uploads of immutable `0.0.0` placeholders. npm requires an existing package before trusted publishing can be configured. The former bootstrap-only code is replaced by real release packaging; do not recreate or overwrite those versions.

`node packages/binpm/scripts/publish.mjs --print-trust-commands` prints one `npm trust github` command per package for the existing workflow identity. Run those commands with npm `11.15.0` or later when configuring publishers. Saving the configuration does not verify hosted publication.

## Change Triggers

Update this contract, `project-binpm.md`, affected workflow/CI path rules, and public installation/release guidance when package names, targets, publisher identity or release flow changes. Update instruction files only when development procedures, directory ownership or repository/domain rules change, following the root instruction-update policy.

## References

- [Project index](project-binpm.md)
- [binpm Rust foundation](crates-binpm-foundation.md)
- [Repository workflow](repository-workflow-contract.md)
- [clibox npm distribution](packages-clibox-distribution-contract.md)
- [npm trust](https://docs.npmjs.com/cli/v11/commands/npm-trust/)
