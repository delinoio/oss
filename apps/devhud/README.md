# DevHud feasibility gate

This package is only the non-product probe for the DevHud CEF feasibility gate. It contains no production tool, release publication automation, mobile shell, widget registration, updater runtime, public API, CLI, or deep link.

The frontend exercises bundled-asset startup and an allowed/denied Tauri IPC pair. The typed harness in `src/probe` is reusable by platform drivers for the remaining gate scenarios. Desktop builds select the pinned upstream CEF runtime with its sandbox feature; future iOS and Android targets select Tauri's standard WRY-backed system webviews from this same Rust crate. The `macos-gate` feature adds only the macOS feasibility integrations needed to exercise the menu-bar lifecycle, global shortcut, launch-at-login, themes, DevTools, and fatal-process behavior.

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
- `pnpm test:macos-gate-contract`

On a native macOS host, run `pnpm gate:macos --target <aarch64-apple-darwin|x86_64-apple-darwin> --evidence <output.json>`. The command builds and mounts a target-specific DMG, validates a signed Tauri updater bundle, executes three startup/shutdown cycles plus fatal initialization and renderer-termination scenarios, checks for orphaned CEF helpers, and writes only path-free safe evidence. It uses Developer ID credentials when both certificate inputs are available; otherwise it verifies an ad hoc-signed, sign-ready bundle. Packages and signing inputs are never published by the gate.

The isolated workflow executes this gate natively on macOS 14+ x64 and ARM64 runners. The exact upstream pin includes Tauri's macOS `TerminationSignals` target-guard correction, so the workflow must reach the native gate before any runtime, DMG, updater, signing, or process condition is claimed. Fatal renderer-termination observation remains blocked on Windows and Linux; see `docs/apps-devhud-foundation.md`.
