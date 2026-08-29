# DevHUD Native Messaging host

This desktop-only broker maintains Chrome's Native Messaging stdio connection and forwards authenticated, bounded `v1` requests to the running DevHUD app over its per-user socket or named pipe. It never calls DevHUD API or GitHub and never stores browser context.

The app invokes `register <absolute-host-path>` on launch. `unregister` first invalidates authenticated sessions in a running app, then removes pairing credentials, the user manifest, and the Windows registry key idempotently.

Run `pnpm --filter devhud test:native:ipc` for the shared app/host IPC contract and `cargo test -p devhud-native-messaging-host` for the host unit and registration fixtures. CI performs installer lifecycle checks only inside disposable package layouts and never changes a contributor's installed host registration.
