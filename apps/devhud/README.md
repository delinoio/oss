# DevHud

This package is the local DevHud application. Desktop builds use the exact pinned upstream Tauri CEF runtime with its sandbox enabled; future iOS and Android targets use Tauri's standard system webviews from the same Rust crate.

The frontend loads only bundled assets. It reads runtime information through `get_runtime_info` and persists only the versioned `devhud.settings.v1` and `devhud.widget-configuration.v1` records through four record-specific native commands. External navigation, popups, downloads, remote frontend resources, undeclared native commands, broad filesystem/default-store authority, and broad application capabilities remain disabled.

Package-local deterministic checks:

- `pnpm build`
- `pnpm build:preview`
- `pnpm typecheck`
- `pnpm lint`
- `pnpm test`
- `pnpm test:build`
- `pnpm check:contracts`
- `pnpm check:locks`
- `pnpm check:rust`
- `pnpm smoke:desktop`

The production desktop shell is tray/menu-bar resident and creates no persistent Dock/taskbar item. It provides close-to-tray HUD/settings windows, transactional structured shortcuts, opt-in autostart, pointer-monitor HUD placement, focus/toggle/hide behavior, technical-preview DevTools, and a typed update action that has no network implementation yet. The internal typed tool registry has an empty production registration. Local settings retain System/Light/Dark theme, launch-at-login preference, and an optional validated structured shortcut; widget configuration retains validated slot-to-stable-`toolId` references. Future schema versions are left untouched, while corrupt data provides reset guidance without exposing its contents.

No production tool, visible widget, updater network implementation, public release automation, public API, CLI, or deep link is included. See `docs/apps-devhud-foundation.md` for the complete contract.
