# DeliDev desktop

## Desktop platform limitation

Windows desktop runs Chromium without its process sandbox because the current upstream CEF runtime does not support an executable-host sandbox broker. This is an explicit platform exception; native IPC restrictions and private browser-profile storage still apply. macOS and Linux require the Chromium process sandbox. Linux file selection requires an XDG desktop portal and portal backend; confirmation dialogs require `zenity`.

Maintainer builds download and verify the pinned Tauri CLI automatically. Use `node scripts/tauri-cli.mjs --source --print-path` from the repository root only when explicitly choosing a local source build; CI always requires the published binary.

Linux AppImage users need an XDG desktop portal service and implementation plus `zenity` for file and confirmation dialogs. On Ubuntu 24.04, install them with `sudo apt install xdg-desktop-portal xdg-desktop-portal-gtk zenity`. Debian packages declare these dialog dependencies.
