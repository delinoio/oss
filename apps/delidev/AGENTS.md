# apps/delidev working instructions

- Follow root and parent instructions.
- This directory owns the DeliDev desktop shell and its native host.
- Read the [project index](../../docs/project-delidev.md) and [desktop contract](../../docs/apps-delidev-desktop-contract.md).
- Keep development on fixed loopback port 46311 and fail on conflicts.
- Run package-local `pnpm test` for frontend changes and root `cargo test` for Rust changes. Verify native launch separately from builds.
