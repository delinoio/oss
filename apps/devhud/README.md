# DevHUD desktop foundation

This workspace contains the local-only React/Rsbuild shell and Rust/Tauri CEF desktop host. The authoritative runtime revision, resolved package versions, CEF archives, and checksums are recorded in `cef-pins.json`; `platforms.json` records the six native desktop definitions.

From the repository root:

```sh
pnpm --filter devhud dev
pnpm --filter devhud build
pnpm --filter devhud test
pnpm --filter devhud verify:pins
pnpm --filter devhud smoke:platform
```

Development binds only `127.0.0.1:46305` and fails if the port is occupied. Platform smoke requires a native production artifact and certifies only macOS 13+, Windows 10 22H2+, or Ubuntu 22.04+ X11 on the matching architecture. On Linux, pass `-- --artifact /usr/bin/devhud` for an installed package or an equivalent root-prepared layout; the CEF SUID sandbox must be owned by `root:root` with mode `4755`. XWayland is best effort; native Wayland is unsupported.

See [`docs/apps-devhud-foundation.md`](../../docs/apps-devhud-foundation.md) for the full contract and current limitations.
