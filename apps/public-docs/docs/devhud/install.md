# Install and Verify DevHud

DevHud is distributed as a coordinated release. A release is public only when its desktop packages, mobile stores, Chrome extension, API, updater, and documentation have passed their independent checks.

## Desktop

Supported systems are macOS 13 or later, Windows 10 22H2 or later, and Ubuntu 22.04 LTS using X11. Releases provide x64 and Arm64 artifacts where listed: macOS disk images, Windows MSI or NSIS installers, and Ubuntu AppImage or Debian packages. Native Wayland is not supported.

Download from [Delino OSS GitHub Releases](https://github.com/delinoio/oss/releases). Verify the published SHA-256 checksums and signed release evidence before opening the installer. Updates retain the package type you installed.

## Mobile stores

Install iOS 16 or later from the Apple App Store, or Android 10/API 29 or later from Google Play. Mobile updates are store-managed. Both packages include the optional one-Deck home-screen widget.

## Chrome extension

Install DevHud from the Chrome Web Store. The extension is a permission-scoped context picker: it does not continuously observe pages, and it does not collect cookies, storage, console output, or network traffic. After an explicit picker gesture, it scans up to 10,000 candidate elements across the active page, then retains and persists only the selected, sanitized context. A Chrome-assisted RealQA draft includes that browser context by default when submitted, so the redacted URL, page title, user agent, viewport and bounds, accessibility values, and sanitized markup may be published in the GitHub issue. Review the draft and use its browser-context removal control before submitting if you do not want to share those details. Pair it from DevHud Settings after installing the desktop app.

## Verification checklist

- Confirm the download source and platform match the release notes.
- Confirm the checksum and platform signature where your operating system provides one.
- Open DevHud and complete first-run setup before pairing Chrome or enabling a widget.
- If a release is under store review, wait for the coordinated release rather than treating one package as generally available.

See [Releases](releases), [Security](security), and [Support](support).
