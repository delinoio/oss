# cachecow

CacheCow (`ccow`) prepares independent, writable build caches for Git worktrees with filesystem copy-on-write. It provides Cargo, Rspack and Rsbuild presets, optional PATH shims, and shell integration.

CacheCow 0.1.0 is unreleased. Building this crate does not establish native filesystem, process-lifecycle or performance acceptance.

- Project index: [docs/project-cachecow.md](../../docs/project-cachecow.md)
- Command contract: [docs/crates-cachecow-foundation.md](../../docs/crates-cachecow-foundation.md)
- Complete requirements: [docs/crates-cachecow-requirements.md](../../docs/crates-cachecow-requirements.md)
- Distribution: [docs/packages-ccow-distribution-contract.md](../../docs/packages-ccow-distribution-contract.md)
- Public guides: [apps/public-docs/docs/cachecow](../../apps/public-docs/docs/cachecow)

Build and test from the repository root:

```sh
cargo build -p cachecow --bin ccow
cargo test -p cachecow
```

Licensed under the Apache License, Version 2.0. See [LICENSE](../../LICENSE) and [NOTICE](../../NOTICE).
