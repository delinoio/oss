# async-commit-hook release boundary

- `release-metadata.json`, the Go version constant, app and client versions must match exactly.
- Archive bytes must be reproducible for identical executable bytes: fix both tar member and gzip-header metadata, omit gzip filenames, and retain fixed ZIP entry metadata.
- Build all six targets with CGO disabled. Every archive contains exactly one regular `ach` or `ach.exe` file.
- Publish only through the manually dispatched `release-async-commit-hook.yml` from `main`, after validation, archive checksums and Sigstore signing. The updater pins that exact workflow identity.
- Before signing and immediately before release creation, resolve the remote version tag (including annotated/nested tags) to the exact validated commit; reject mismatches and lookup errors without changing tags.
- Dry runs never sign, create tags/releases, update a tap or deploy Pages. They emit unsigned archives and an explicit validation record.
- Keep GitHub publication, Homebrew and Pages in separate dependent jobs so Re-run failed jobs preserves successful immutable publication. An already matching Homebrew formula is a successful no-op.
- Resume only an unpublished draft whose exact ownership marker, target commit, version and workflow run ID match the current run. Reread ownership before replacing partial uploads, reject unexpected assets, and verify the full uploaded names, sizes, SHA-256 digests and uploaded states before publication. Published releases and unowned drafts are never overwritten.
- Release artifact/installer fixtures under `scripts/release` use Node built-ins only and run without dependency installation. YAML workflow fixtures belong to `scripts/ci` and run after the frozen workspace install.
- Do not label cross-compilation as native integration validation. Preserve the owner-approved exclusions in the evidence document.
- Public installers are authored under `apps/async-commit-hook/public`; release preparation copies those exact bytes into downloadable assets.
