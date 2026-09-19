# async-commit-hook release boundary

- `release-metadata.json`, the Go version constant, app and client versions must match exactly.
- Build all six targets with CGO disabled. Every archive contains exactly one regular `ach` or `ach.exe` file.
- Publish only through the manually dispatched `release-async-commit-hook.yml` from `main`, after validation, archive checksums and Sigstore signing. The updater pins that exact workflow identity.
- Dry runs never sign, create tags/releases, update a tap or deploy Pages. They emit unsigned archives and an explicit validation record.
- Do not label cross-compilation as native integration validation. Preserve the owner-approved exclusions in the evidence document.
- Public installers are authored under `apps/async-commit-hook/public`; release preparation copies those exact bytes into downloadable assets.
