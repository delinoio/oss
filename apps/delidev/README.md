# DeliDev desktop

## Desktop platform limitation

Windows desktop runs Chromium without its process sandbox because the current upstream CEF runtime does not support an executable-host sandbox broker. This is an explicit platform exception; native IPC restrictions and private browser-profile storage still apply. macOS and Linux require the Chromium process sandbox. Linux file selection requires an XDG desktop portal and portal backend; confirmation dialogs require `zenity`.

Maintainer builds download and verify the pinned Tauri CLI automatically. Use `node scripts/tauri-cli.mjs --source --print-path` from the repository root only when explicitly choosing a local source build; CI always requires the published binary.

Linux AppImage users need an XDG desktop portal service and implementation plus `zenity` for file and confirmation dialogs. On Ubuntu 24.04, install them with `sudo apt install xdg-desktop-portal xdg-desktop-portal-gtk zenity`. Debian packages declare these dialog dependencies.

## Authenticated operations

The server token and paired device credentials authorize product operations. Roles and resource attribution do not add access restrictions. Starts, retries and cleanup continue when historical ownership or cleanup cannot be confirmed; diagnostics record the uncertainty. DeliDev preserves Stop intent and historical execution records. It signals only processes whose native handles it retains, and it does not report unconfirmed cleanup as successful. New attempts can overlap older processes.
