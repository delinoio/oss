# DeliDev Desktop Shell

## Ownership

`apps/delidev` owns the only DeliDev executable. See the [project index](project-delidev.md).

## Behavior

Launch one native window titled `DeliDev`, initially 1280 by 820 pixels, with standard operating-system decorations and resizing. The content area is blank: no text, buttons, navigation, product menus or dialogs. Closing the last window exits the application. React renders an empty root.

Keep the `io.delino.delidev` application identifier, React/Rsbuild frontend, pinned Tauri/CEF runtime, CEF helper entry point and macOS 13.0 minimum. The webview is incognito and has no application commands or granted capabilities. Preserve CEF's required sandbox on macOS/Linux and its existing Windows automatic-policy limitation.

There is no CLI, server, Worker, mobile application, RPC schema, API client, sidecar, tray, widget, notification, account access, updater or product data migration. The shell does not inspect or erase previous user data or credentials. CEF infrastructure processes are part of rendering, not product Workers.

## Development

From `apps/delidev`, run `pnpm dev:desktop` to start the native shell and its frontend. `pnpm dev` serves the frontend at `http://127.0.0.1:46311`; port conflicts fail. The shared pinned Tauri CLI owns native setup and helper assembly. No Go binary or signing credentials are required for the shell.

`pnpm build` builds the frontend. `pnpm build:native` builds the native executable without publishing a distribution. Required CEF notices and repository license notices remain available. DeliDev release and feature automation are removed.

## Validation

Run `pnpm test` in the app and `cargo test` at the repository root. Frontend type checking and builds do not establish native acceptance. Verify a real native launch, empty content, resizing, window close and process exit. Record revision, commands, outcomes and unperformed platform checks in commit/PR or CI records. Do not add generated `dist` output to Git; remove it after validation.
