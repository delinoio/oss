# DevHUD Native Messaging host

This desktop-only broker maintains Chrome's Native Messaging stdio connection and forwards authenticated, bounded `v1` requests to the running DevHUD app over its per-user socket or named pipe. It never calls DevHUD API or GitHub and never stores browser context.

The app invokes `register <absolute-host-path>` on launch. `unregister` removes the user manifest and Windows registry key idempotently.
