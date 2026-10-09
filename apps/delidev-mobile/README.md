# DeliDev mobile

DeliDev mobile is a remote client for an existing DeliDev server. It supports
Sessions, Inbox and Settings on iOS18 or later and Android12 or later.

Add a named HTTPS server profile in Settings, then paste the complete client
pairing document from that server. Pairing credentials are stored in the device's
protected storage. Switching profiles changes the connected server. Forgetting a
profile removes it from this device; remote sessions continue running.

Create Worktree or General Chat sessions with saved Projects, Agents and Runners.
Open a conversation to send text, steer a queued input, inspect requests, or stop
and resume the selected remote session. A connection interruption preserves the
original request. Inspect current state before choosing its exact retry.

The app synchronizes while it is in the foreground. Foreground notifications are
optional and do not mark Inbox entries as read. Configure language and theme in
Settings. Mobile does not start a local server or Worker.

Internal beta distribution and real device/account acceptance are not yet
verified. Store availability is not claimed by a fixture or build result.

The server must allow the mobile webview origins `tauri://localhost` for iOS and
`http://tauri.localhost` for Android alongside its existing trusted clients.
Keep exact origins and normal certificate verification. VPN connectivity and
server setup belong to the user; pairing does not install or start services.
