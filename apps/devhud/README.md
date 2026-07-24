# DevHud

This package is the local DevHud application foundation. Desktop builds use the exact pinned upstream Tauri CEF runtime with its sandbox enabled; future iOS and Android targets use Tauri's standard system webviews from the same Rust crate.

The frontend loads only bundled assets and reads runtime information through the scoped `get_runtime_info` command. External navigation, popups, downloads, remote frontend resources, undeclared native commands, and broad application capabilities remain disabled.

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

The current foundation contains no production tool, release automation, mobile shell, widget registration, updater implementation, public API, CLI, or deep link. See `docs/apps-devhud-foundation.md` for the complete contract.
