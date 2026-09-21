# async-commit-hook public documentation

- Follow docs/apps-async-commit-hook-docs-foundation.md. This Rspress app owns documentation and verified installer assets assembled at https://oss.delino.io/async-commit-hook, never the results UI or local RPC transport.
- Preserve the complete migrated guides, examples, limitations and recovery behavior. Keep internal implementation contracts and repository paths in root docs; public user-owned configuration paths remain supported interfaces.
- Keep all stable routes in grouped top navigation and the sidebar and retain /docs section-link migration through the explicit route allowlist. Never forward pairing/run fragments from the retired hosted UI into a local service.
- Canonical installer sources are public/install.sh and public/install.ps1. Preserve argument validation and checksum/Sigstore verification; release archives and documentation must copy those exact bytes.
- Primary install commands use the downloaded installers' synchronized defaults; primary self-update selects the highest published stable release. Keep explicit-version and rollback guidance separate with MAJOR.MINOR.PATCH placeholders, and enforce these rules against the rendered production pages.
- Release Project synchronizes this app's manifest and installer defaults with the local UI, client, Go version and release metadata in one version-only commit.
- Development and preview use the shared fixed loopback wrapper on 46310 and 46281, failing on conflicts. Package-local pnpm test builds and validates pages, legacy routes, public content and installer bytes.
- The package output is an input to the shared `public-docs` publication; this app has no standalone Pages deployment. Preserve truthful public-release and native-platform validation limitations, and decommission former standalone hosting only after consolidated route and installer checks pass. Do not add redirects.
