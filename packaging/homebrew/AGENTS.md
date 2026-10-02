# Homebrew packaging rules

- Follow the root license and release contracts and each project's distribution contract. Templates are repository-owned Apache-2.0 source; retain upstream notices in installed archives.
- Runmoor uses only its published `runmoor-darwin-arm64.tar.gz` archive on macOS 14+ Apple Silicon. Do not add Intel, Linux Homebrew, a source build, bundled Tart, automatic configuration or a Homebrew service definition.
- Render Runmoor through `scripts/release/update-homebrew.sh` with an exact stable version, canonical version-bound URL and SHA-256. Reject unsupported platform inputs, version downgrades and changed Formula bytes for an existing version; identical retries are no-ops.
- Runmoor publication follows `docs/cmds-runmoor-foundation.md`: verify public source/tag identity and all eight signed assets, test the exact Formula with Homebrew on an ephemeral Apple Silicon runner, then obtain a fresh tap-only bot token. Never change a developer's Homebrew installation for release validation.
- Run relevant release fixtures and workflow contracts when changing templates or publication. Keep README and public installation guidance synchronized with actual supported distribution channels.

- clibox supports macOS x64/arm64 only, using its version-bound signed Darwin archives. Preserve full LICENSE, NOTICE and LICENSE.fspy during installation. Test and audit the identical Formula on both native Mac architectures before acquiring tap-only credentials; reject downgrades and changed same-version Formula bytes. Never validate by mutating a developer's Homebrew installation. Follow `docs/packages-clibox-distribution-contract.md`.
- Select clibox's architecture-specific `url` and `sha256` with `Hardware::CPU.arm?` conditionals; Homebrew's `on_arm`/`on_intel` blocks do not permit those source declarations. Keep rendered Formula lines within the strict audit limit and validate both branch selections in release fixtures.
