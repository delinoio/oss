# async-commit-hook release contract

## Scope
The command release boundary owns six `ach` archives, shell/PowerShell installers, a Homebrew formula, artifact verification and explicit self-update. The static app and public `/docs` ship through the same manually dispatched release workflow. The initial version is `0.1.0`.

## Runtime and Language
The Go binary uses CGO-free builds for darwin/linux/windows and amd64/arm64. Python assembles and inspects archives; GitHub Actions orchestrates validation, Sigstore signing, publication and Cloudflare Pages. A release archive contains exactly one regular `ach` or `ach.exe`, with no executable alias. Identical executable bytes produce identical archives: Unix tar members and gzip headers have fixed timestamps, gzip carries no filename, and ownership/mode metadata is fixed; Windows ZIP entries retain their fixed timestamp and mode. Archive checksums therefore do not depend on the time a retry runs.

## Users and Operators
Direct-install users, Homebrew users and authorized release maintainers. The owner explicitly excluded real six-target machine qualification and actual publication/deployment from the September 2026 implementation. Cross-build results must never be described as native integration results.

## Interfaces and Contracts
`packaging/async-commit-hook/release-metadata.json`, the Go version constant and both package versions must match. Release identity is `async-commit-hook@v<MAJOR.MINOR.PATCH>`. Archives are named `ach-<goos>-<goarch>.tar.gz` or `ach-windows-<goarch>.zip`.
The shell installer accepts `--version MAJOR.MINOR.PATCH`, overriding `ACH_VERSION` and then the bundled default. Both installers require canonical three-part numeric versions without leading zeroes. Missing values, unknown shell arguments and malformed versions fail before any download or installation work.

`scripts/release/build-async-commit-hook.py --validate` checks versions and the exact six-target set. `--output <empty-directory>` cross-builds all targets, assembles archives, copies the public installers, generates `async-commit-hook.rb`, `compatibility.json` and `SHA256SUMS`. This local dry run neither signs nor publishes. Compatibility output explicitly records unsigned/unpublished status and the real-machine exclusion.

`.github/workflows/release-async-commit-hook.yml` accepts only manual dispatch on `main`, with an exact source version and `dry_run=true` by default. Validation generates the administrator embed, runs the repository Go suite, race tests, frontend/client tests, protocol checks and release fixtures, then builds all six archives. Dry-run artifacts stay in the temporary runner directory and are not uploaded; dry runs cannot sign, push a tag/tap, create a release or deploy a site. Only an explicitly publishing run uploads intermediate build artifacts, with seven-day retention, for its downstream signing/deployment jobs.

Publication requires the `async-commit-hook-release` environment. The publish job receives OIDC and repository release permissions, signs every artifact and the checksum manifest using the existing fail-closed checksum helper, then publishes an immutable versioned GitHub Release through `scripts/release/publish-async-commit-hook.py`. Completed releases are rejected.

The publisher first creates an unpublished draft with an exact ownership marker containing the repository, workflow, workflow run ID and validated commit. A retry of the same run can resume that draft after an interrupted creation response or partial asset upload. It requires matching tag, target commit, title, body and draft status, rereads ownership before upload, rejects unexpected assets, and replaces only the expected draft assets. Every archive, installer, formula, compatibility record, checksum manifest and corresponding Sigstore bundle must be present locally as a regular nonsymlink file. Before publication, the complete paginated remote asset set must match all expected names, sizes, SHA-256 digests and uploaded states, and the remote tag is verified again. Lookup failures cannot be interpreted as an absent release. No completed release or unowned draft is modified.

Before signing, `--verify-tag COMMIT` reads the remote version ref and its peeled target. The publisher invokes the same verifier before draft creation/resumption and immediately before publishing. An absent tag is allowed for GitHub creation at the validated commit; an existing lightweight, annotated or nested tag must resolve exactly to that commit. Mismatches and lookup errors fail closed without moving or creating a tag. The GitHub release target field is not used as an existing-tag identity check. The independent `homebrew` job depends on successful `publish`, downloads the same workflow run's validated formula and uses `HOMEBREW_TAP_GH_TOKEN` only for the explicit `delinoio/homebrew-tap` update. An identical remote formula succeeds without another commit or push. The subsequent `deploy` job depends on `homebrew` and uses `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID` and `ACH_PAGES_PROJECT`, with pinned Wrangler, to deploy the validated static bundle. Never put these credentials in source, installers or browser bundles. No credentials were created for this implementation.

After an interrupted draft upload or a downstream Homebrew or Pages failure, use GitHub Actions **Re-run failed jobs** on that same workflow run within the seven-day artifact retention window. Successful GitHub publication remains complete and is not repeated; a Pages-only retry also preserves successful Homebrew publication. Re-running all jobs or dispatching another publication for an already published version intentionally fails the immutable-release guard. A new workflow run also cannot take over an earlier run's draft. Legacy drafts without the ownership marker, modified drafts and drafts with unexpected assets require maintainer inspection; the workflow never deletes or claims them automatically. If retained artifacts have expired, stop and recover the original validated artifacts through maintainer review; never overwrite the existing release.

## Storage
Installed executable replacement uses a same-volume candidate, a retained previous executable, a durable account-scoped update journal and a consistent SQLite `VACUUM INTO` backup plus owned evidence. State version 1 is validated before use; unknown versions fail without conversion. Backups remain account-private and exclude filesystem symlink traversal.

## Security
Installers and self-update verify both the checksum manifest and chosen archive's Sigstore bundle before extraction or execution. The exact trusted certificate identity is `https://github.com/delinoio/oss/.github/workflows/release-async-commit-hook.yml@refs/heads/main`, with issuer `https://token.actions.githubusercontent.com`. The Go verifier validates trusted roots, certificate identity, transparency evidence and artifact integrity. A valid signature belonging to another workflow is insufficient.

Direct self-update refuses active components, pending work and Homebrew-owned paths. Candidate `ach version --json` must match the requested release. A lifecycle lock excludes concurrent starts and mode/port/state changes; a pending journal requires explicit recovery. Windows uses a separate replacement helper after the original process exits. Recovery verifies journal-bound digests and restores the previous executable when replacement was interrupted. No background automatic update is installed.

## Logging
Archive generation emits structured target, artifact and SHA-256 evidence. Runtime failures use bounded stable codes. Do not log tokens, pairing material, resolved secrets or full environment variables.

## Build and Test
Run `node --test scripts/release/async-commit-hook.test.mjs` for dependency-free artifact/installer fixtures and offline publication recovery fixtures. Publication fixtures intercept every GitHub command and tag lookup; they cover partial uploads, lost creation responses, changed signatures on retry, wrong ownership/commit, completed releases, lookup/tag failures and incomplete or mismatched remote assets without network mutations. After the frozen workspace install, run `node --test scripts/ci/async-commit-hook-release.test.mjs`, `pnpm ci:workflows` and `pnpm ci:contracts` for YAML workflow and publication-retry contracts. Also run Go updater/installer ownership fixtures and the six-target builder. Signature tests include real upstream Sigstore verification evidence with an untrusted workflow, forged bundles and tampered bytes. Shell installer tests use isolated download/signature fixtures to prove fail-closed publication ordering. Windows installation and replacement receive cross-build/source checks here, not a falsely claimed Windows execution result.

## Change Triggers
Update this contract, packaging AGENTS, version metadata, evidence and public upgrade/compatibility guidance when artifact names, trust identity, update ownership or deployment behavior changes.

## References
- [Project](project-async-commit-hook.md)
- [Command contract](cmds-async-commit-hook-contract.md)
- [Implementation evidence](cmds-async-commit-hook-evidence.md)

Windows helpers retain a separate UUID-scoped cleanup record before creating the replacement sibling. Successful installation can remove its update journal without losing the helper path, authenticated digest or process birth identity. Subsequent service opens retry cleanup under the account lifecycle lock, wait for the helper to exit, reject changed/nonregular files, and remove the cleanup record only after deleting the helper. Cleanup failure remains recorded and emits a stable warning without blocking ordinary queries.

After its original parent exits, a helper acquires the account lifecycle lock and rereads the journal. Its bytes must match the prepared journal observed before waiting. Recovery can therefore win that lock and remove the journal without a delayed helper resurrecting it; a later update's different journal is equally protected. Journal comparison, helper preparation and replacement remain inside the same lock. Stale helpers exit with `update-superseded`.
