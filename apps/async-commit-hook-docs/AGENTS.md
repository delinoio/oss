# async-commit-hook public documentation

- Follow docs/apps-async-commit-hook-docs-foundation.md. This Rspress app owns documentation and verified installer URLs at https://ach.delino.io, never the results UI or local RPC transport.
- Preserve the complete migrated guides, examples, limitations and recovery behavior. Keep internal implementation contracts and repository paths in root docs; public user-owned configuration paths remain supported interfaces.
- Keep all stable routes in navigation/sidebar and retain /docs section-link migration through the explicit route allowlist. Never forward pairing/run fragments from the retired hosted UI into a local service.
- Canonical installer sources are public/install.sh and public/install.ps1. Preserve argument validation and checksum/Sigstore verification; release archives and documentation must copy those exact bytes.
- Development and preview use the shared fixed loopback wrapper on 46310 and 46281, failing on conflicts. Package-local pnpm test builds and validates pages, legacy routes, public content and installer bytes.
- Hosting remains a separate manual release step. Preserve truthful public-release and native-platform validation limitations.
