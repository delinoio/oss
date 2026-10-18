# DeliTools

Blank desktop, Android and iOS application shell.

Run `pnpm dev:desktop` for desktop. Generate mobile projects with `pnpm mobile:android:init` or `pnpm mobile:ios:init`, then use `pnpm dev:android` or `pnpm dev:ios`. Android requires the SDK/NDK and Java 17. iOS requires macOS and Xcode.

Run `pnpm test` here and `cargo test` at the repository root. See the [application contract](../../docs/apps-delitools-foundation.md) for mobile signing, environment configuration and test-channel distribution.
