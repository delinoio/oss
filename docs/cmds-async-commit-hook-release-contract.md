# async-commit-hook release contract

## Scope
The command release boundary owns six `ach` archives, shell/PowerShell installers, a Homebrew formula, artifact verification and explicit self-update. The static app and public `/docs` ship through the same manually dispatched release workflow. The initial version is `0.1.0`.

## Runtime and Language
The Go binary uses CGO-free builds for darwin/linux/windows and amd64/arm64. Python assembles and inspects archives; GitHub Actions orchestrates validation, Sigstore signing, publication and Cloudflare Pages. A release archive contains exactly one regular `ach` or `ach.exe`, with no executable alias.

## Users and Operators
Direct-install users, Homebrew users and authorized release maintainers. The owner explicitly excluded real six-target machine qualification and actual publication/deployment from the September 2026 implementation. Cross-build results must never be described as native integration results.

## Interfaces and Contracts
`packaging/async-commit-hook/release-metadata.json`, the Go version constant and both package versions must match. Release identity is `async-commit-hook@v<MAJOR.MINOR.PATCH>`. Archives are named `ach-<goos>-<goarch>.tar.gz` or `ach-windows-<goarch>.zip`.

`scripts/release/build-async-commit-hook.py --validate` checks versions and the exact six-target set. `--output <empty-directory>` cross-builds all targets, assembles archives, copies the public installers, generates `async-commit-hook.rb`, `compatibility.json` and `SHA256SUMS`. This local dry run neither signs nor publishes. Compatibility output explicitly records unsigned/unpublished status and the real-machine exclusion.

`.github/workflows/release-async-commit-hook.yml` accepts only manual dispatch on `main`, with an exact source version and `dry_run=true` by default. Validation generates the administrator embed, runs the repository Go suite, race tests, frontend/client tests, protocol checks and release fixtures, then builds all six archives. Dry-run artifacts stay in the temporary runner directory and are not uploaded; dry runs cannot sign, push a tag/tap, create a release or deploy a site. Only an explicitly publishing run uploads intermediate build artifacts, with seven-day retention, for its downstream signing/deployment jobs.

Publication requires the `async-commit-hook-release` environment. The publish job receives OIDC and repository release permissions, signs every artifact and the checksum manifest using the existing fail-closed checksum helper, then creates an immutable versioned GitHub Release. An existing release is rejected. The independent `homebrew` job depends on successful `publish`, downloads the same workflow run's validated formula and uses `HOMEBREW_TAP_GH_TOKEN` only for the explicit `delinoio/homebrew-tap` update. An identical remote formula succeeds without another commit or push. The subsequent `deploy` job depends on `homebrew` and uses `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID` and `ACH_PAGES_PROJECT`, with pinned Wrangler, to deploy the validated static bundle. Never put these credentials in source, installers or browser bundles. No credentials were created for this implementation.

After a downstream Homebrew or Pages failure, use GitHub Actions **Re-run failed jobs** on that same workflow run within the seven-day artifact retention window. Successful GitHub publication remains complete and is not repeated; a Pages-only retry also preserves successful Homebrew publication. Re-running all jobs or dispatching another publication for an existing version intentionally fails the immutable-release guard. If retained artifacts have expired, stop and recover the original validated artifacts through maintainer review; never overwrite the existing release.

## Storage
Installed executable replacement uses a same-volume candidate, a retained previous executable, a durable account-scoped update journal and a consistent SQLite `VACUUM INTO` backup plus owned evidence. State version 1 is validated before use; unknown versions fail without conversion. Backups remain account-private and exclude filesystem symlink traversal.

## Security
Installers and self-update verify both the checksum manifest and chosen archive's Sigstore bundle before extraction or execution. The exact trusted certificate identity is `https://github.com/delinoio/oss/.github/workflows/release-async-commit-hook.yml@refs/heads/main`, with issuer `https://token.actions.githubusercontent.com`. The Go verifier validates trusted roots, certificate identity, transparency evidence and artifact integrity. A valid signature belonging to another workflow is insufficient.

Direct self-update refuses active components, pending work and Homebrew-owned paths. Candidate `ach version --json` must match the requested release. A lifecycle lock excludes concurrent starts and mode/port/state changes; a pending journal requires explicit recovery. Windows uses a separate replacement helper after the original process exits. Recovery verifies journal-bound digests and restores the previous executable when replacement was interrupted. No background automatic update is installed.

## Logging
Archive generation emits structured target, artifact and SHA-256 evidence. Runtime failures use bounded stable codes. Do not log tokens, pairing material, resolved secrets or full environment variables.

## Build and Test
Run `node --test scripts/release/async-commit-hook.test.mjs`, `pnpm ci:workflows`, `pnpm ci:contracts`, Go updater/installer ownership fixtures, and the six-target builder. Signature tests include real upstream Sigstore verification evidence with an untrusted workflow, forged bundles and tampered bytes. Shell installer tests use isolated download/signature fixtures to prove fail-closed publication ordering. Windows installation and replacement receive cross-build/source checks here, not a falsely claimed Windows execution result.

## Change Triggers
Update this contract, packaging AGENTS, version metadata, evidence and public upgrade/compatibility guidance when artifact names, trust identity, update ownership or deployment behavior changes.

## References
- [Project](project-async-commit-hook.md)
- [Command contract](cmds-async-commit-hook-contract.md)
- [Implementation evidence](cmds-async-commit-hook-evidence.md)
