# DevHud feasibility gate

This package is only the common, non-product probe for the DevHud CEF feasibility gate. It contains no production tool, release automation, mobile shell, widget registration, updater implementation, public API, CLI, or deep link.

The frontend exercises bundled-asset startup and an allowed/denied Tauri IPC pair. The typed harness in `src/probe` is reusable by platform drivers for the remaining gate scenarios. Desktop builds select the pinned upstream CEF runtime with its sandbox feature; future iOS and Android targets select Tauri's standard WRY-backed system webviews from this same Rust crate.

Package-local deterministic checks:

- `pnpm build`
- `pnpm typecheck`
- `pnpm lint`
- `pnpm test`
- `pnpm test:build`
- `pnpm check:contracts`
- `pnpm check:locks`
- `pnpm check:rust`
- `pnpm smoke:desktop`

The gate is not a production application and is not a claim that the cross-platform matrix has passed. The pinned upstream revision currently blocks fatal renderer-termination observation on Windows and Linux; see `docs/apps-devhud-foundation.md`.
