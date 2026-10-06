# Repository License Contract

## Ownership

Delino-owned source in this repository is Apache-2.0 licensed. The root `LICENSE` contains the complete terms and `NOTICE` identifies the copyright holder. Delino-owned Rust crates and npm workspaces declare `Apache-2.0`; publishable archives include the complete terms. The root Cargo workspace default is Apache-2.0.

## Imported Material

The imported VoidZero fspy, materialized-artifact and vt crates keep their original MIT text, copyright notices and explicit MIT Cargo metadata. The vendored Microsoft Detours source keeps its separate MIT notices. Bundled Noto Sans KR fonts keep their OFL terms. A repository-wide default does not relicense these works. Retain their notices in any distribution that carries their bytes, including pnport's fspy companion.

The local DeliDev subscription provider and Agent Worker harness marks (OpenAI, Claude, Grok and OpenCode) are imported from LobeHub/lobe-icons at revision `329f378cbd1a88f45b60cd096b9111ce16f3ea39` under MIT. Keep the original license and source/modification notice in `apps/delidev/public/subscription-marks`; frontend distributions copy those notices with the marks. The brand marks identify their respective owners and do not imply endorsement.

## Distribution

New npm, Homebrew, GitHub archive and native Linux packages for Delino-owned products declare Apache-2.0 and carry the complete license text. Native Linux keyring metadata is Apache-2.0. Published versions and immutable existing artifacts retain their historical terms; future versions use the new metadata. Package inspection and installed-consumer tests check license declarations, terms and separate third-party notices.
