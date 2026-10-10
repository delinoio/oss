# crates working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `crates/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

- Follow root `AGENTS.md` and each crate-specific project document.

- Keep repository and domain rules in the appropriate `AGENTS.md` files.

- Write all source and comments in English.

- Prefer enums over free-form strings for stable internal and external contracts.

- Add new crates as explicit workspace members in root `Cargo.toml`.

- Keep crate naming aligned with project IDs when possible.

- Document behavior contracts in project index docs and relevant crate-domain docs before large implementation changes.

- Planned crate paths must not be added as workspace members until the crate skeleton exists.

- Prefer minimal default features and keep optional capabilities opt-in for size-sensitive crates.

- Keep proc-macro crates and runtime crates separated by explicit crate boundaries.

- Test cleanup must remove only the fixture-owned directory, never the process-wide temporary directory or sibling fixtures.

## Owning contracts

- [clibox public documentation foundation](../docs/apps-clibox-docs-foundation.md)
- [DeliDev source ownership and compatibility](../docs/cmds-delidev-structure-contract.md)
- [crates-binpm-foundation](../docs/crates-binpm-foundation.md)
- [crates-cargo-mono-foundation](../docs/crates-cargo-mono-foundation.md)
- [clibox Rust foundation](../docs/crates-clibox-foundation.md)
- [clibox fspy workflow contract](../docs/crates-clibox-fspy-contract.md)
- [crates-devhud-native-messaging-host-contract](../docs/crates-devhud-native-messaging-host-contract.md)
- [Forge Rust Foundation](../docs/crates-forge-foundation.md)
- [fspy source fork contract](../docs/crates-fspy-vendor-contract.md)
- [crates-nodeup-foundation](../docs/crates-nodeup-foundation.md)
- [pnport Rust foundation](../docs/crates-pnport-foundation.md)
- [React Forge Native Contract](../docs/crates-react-forge-contract.md)
- [crates-rustia-core-foundation](../docs/crates-rustia-core-foundation.md)
- [crates-rustia-llm-foundation](../docs/crates-rustia-llm-foundation.md)
- [crates-rustia-macros-foundation](../docs/crates-rustia-macros-foundation.md)
- [crates-serde-feather-core-foundation](../docs/crates-serde-feather-core-foundation.md)
- [crates-serde-feather-macros-foundation](../docs/crates-serde-feather-macros-foundation.md)
- [crates-with-watch-foundation](../docs/crates-with-watch-foundation.md)
- [React Forge Figma Contract](../docs/packages-react-forge-figma-contract.md)
- [React Forge 3D scenes and animation](../docs/packages-react-forge-scene-contract.md)
- [React Forge SFX contract](../docs/packages-react-forge-sfx-contract.md)
- [React Forge Sprite Contract](../docs/packages-react-forge-sprite-contract.md)
- [Project: binpm](../docs/project-binpm.md)
- [Project: cargo-mono](../docs/project-cargo-mono.md)
- [Project: nodeup](../docs/project-nodeup.md)
- [pnport](../docs/project-pnport.md)
- [Project: rustia](../docs/project-rustia.md)
- [Project: serde-feather](../docs/project-serde-feather.md)
- [Project: with-watch](../docs/project-with-watch.md)
- [Repository License Contract](../docs/repository-license-contract.md)
