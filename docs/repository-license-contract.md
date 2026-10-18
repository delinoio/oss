# Repository License Contract

## Ownership

Delino-owned source in this repository is Apache-2.0 licensed. The root `LICENSE` contains the complete terms and `NOTICE` identifies the copyright holder. Delino-owned Rust crates and npm workspaces declare `Apache-2.0`; publishable archives include the complete terms. The root Cargo workspace default is Apache-2.0.

## Imported Material

The imported VoidZero fspy, materialized-artifact and vt crates keep their original MIT text, copyright notices and explicit MIT Cargo metadata. The vendored Microsoft Detours source keeps its separate MIT notices. Bundled Noto Sans KR fonts keep their OFL terms. A repository-wide default does not relicense these works. Retain their notices in any distribution that carries their bytes, including pnport's fspy companion.

## Distribution

New npm, Homebrew, GitHub archive and native Linux packages for Delino-owned products declare Apache-2.0 and carry the complete license text. Native Linux keyring metadata is Apache-2.0. Published versions and immutable existing artifacts retain their historical terms; future versions use the new metadata. Package inspection and installed-consumer tests check license declarations, terms and separate third-party notices.

Future binpm, cargo-mono, nodeup, and with-watch GitHub release archives contain the unchanged platform executable plus the root `LICENSE` and `NOTICE` as regular top-level members. Archive assembly uses those source-checkout texts directly or verifies that staged copies match them. Standalone executable assets and historical published archives retain their existing bytes.

## Rust component integration

- Repository-owned crates declare Apache-2.0; imported VoidZero and Microsoft crates explicitly retain MIT and their upstream notices. Follow `repository-license-contract.md`.
