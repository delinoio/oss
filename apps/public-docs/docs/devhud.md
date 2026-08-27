# DevHud

DevHud is a developer utility for capturing RealQA feedback, following pull-request Decks, and coordinating releases across desktop, mobile, widgets, and the Chrome companion extension.

## Availability

A DevHud version becomes generally available only when its desktop downloads and updater, official API, this documentation, Apple App Store package, Google Play package, and Chrome Web Store extension are all independently public and verified. DevHud does not label a version generally available while one of those surfaces is still waiting for review or publication.

When a coordinated release is available, desktop installers and release evidence are published through [Delino OSS GitHub Releases](https://github.com/delinoio/oss/releases). The mobile app and Chrome extension are distributed through their official stores.

See [Install and Verify](devhud/install), [Using DevHud](devhud/guide), [Privacy](devhud/privacy), [Security](devhud/security), [Support](devhud/support), [Administration](devhud/admin), and [Releases](devhud/releases) for user guidance.

## Desktop packages

DevHud publishes native packages for x64 and Arm64 systems:

- macOS disk images
- Windows MSI and NSIS installers
- Ubuntu AppImage and Debian packages

Each release includes SHA-256 checksums and signed release evidence. Desktop updates use the stable channel only, verify a signed manifest and artifact before asking for installation approval, and retain the installed package type when choosing an update.

## Mobile and browser companion

The iOS and Android packages include their native Deck widgets. Install the Chrome companion extension from the Chrome Web Store and review its requested site access before enabling it; DevHud uses it only for configured browser-context capture.

Store review can take longer than desktop packaging. During that time, the whole coordinated release remains pending; there is no partial or staged general-availability channel.

## Trust and verification

Platform signing and the DevHud updater signature serve different purposes. macOS, Windows, Apple, Android, and Chrome validate their platform packages, while the desktop updater independently validates its stable manifest and downloaded package. GitHub release evidence includes checksums, software bills of materials, provenance, validation records, and signature bundles for maintainers who need to audit a release.
