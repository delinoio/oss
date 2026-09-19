# Releases, verification, and rollback

The initial version is `0.1.0`, with the exact release identity `runlens@v0.1.0`. This documentation describes the release contract; **public release validation is not yet complete**. Only artifacts from a published, fully validated Runlens release are installation candidates.

A regular release requires six native OS/architecture artifacts, SHA256 checksums, Sigstore verification material, actual execution/installation evidence, validated installers and prebuilt Homebrew distribution, and matching documentation. Missing target evidence blocks complete release approval. An unsigned CI/dry-run artifact is not a public-ready release.

Published assets are immutable. Installers reject missing, invalid, mismatched, or unsupported material. Sigstore provides artifact provenance verification; it is not Apple notarization or Windows Authenticode. Those native signing channels are excluded.

## Manual update and rollback

Install an explicit published version using the [installation commands](/installation). Updating or rolling back is another explicit version installation; there is no automatic self-updater. Verify the selected release before replacement. Interrupted installation preserves the existing executable until the verified replacement is ready.

Saved reports are independent of the installed binary. Updates and rollback do not migrate, rewrite, expire, or delete them. An older reader rejects incompatible schemas clearly. CLI and report compatibility are preserved within a product major version; schema versions are independent.

Runlens has no remote feature flags, cohorts, staged activation, or telemetry. Available functionality is determined by the installed version. Support uses [GitHub issues](https://github.com/delinoio/oss/issues), with no fixed release date or support SLA.
