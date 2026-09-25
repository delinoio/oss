# DeliDev desktop client

## Scope
`apps/delidev` owns the React desktop presentation and native Tauri host for DeliDev. The full desktop requirements in issue #964 remain normative; implemented surfaces and remaining native/product work are recorded in the evidence ledger.

## Runtime and Language
React 19.2.8 and TypeScript render the trusted app through Rsbuild. The native Tauri component implements the window, bounded Go sidecar startup and private client bootstrap. Automatic supervision remains pending. Go owns every product operation. The frontend development origin is fixed at `http://127.0.0.1:46311`; conflicts fail. This is a desktop application, not a browser product or deployed website.

The native crate is a root workspace member. Its optional `desktop-host` feature uses Wry from the immutable official Tauri revision `4af26a3f7f8b692d62cca549bbacd93f5ce90b41`; it does not alter DevHud’s CEF feature graph. The default library tests require no display. The executable is `delidev-desktop`, bundle identifier `io.delino.delidev`.

## Users and Operators
One server owner can connect multiple paired desktop clients. The initial prerequisite checklist links to saved settings without installing a harness or overriding existing setup. Automated readiness checks remain pending.

## Interfaces and Contracts
- Use `@delinoio/delidev-api-client` and generated service-specific `@connectrpc/connect-query` descriptors for direct authenticated Connect RPC. No Rust agent traffic proxy or duplicate eligibility/routing engine is permitted.
- Preserve independent outcome, Archive, dispatch and recovery states. Actions use original resource revisions and stable request IDs; uncertain retries reuse the original immutable request, never another mutation identity. Restore cannot imply Resume.
- Keep drafts mounted across settings and supporting-surface navigation. Settings is an accessible modal with Escape, contained focus and focus restoration. Workspace icons have explicit accessible names in addition to appearance.
- Conversation content is inert text. Never render native content as HTML, execute a returned command or open arbitrary links automatically. Transcript tools/artifacts remain distinguishable from user/assistant text.
- Search, activity and inbox use bounded pages. Inbox reading and answering remain separate. Unknown/unsupported native capabilities are explicit and cannot enable an emulated action.
- Native initialization, local server lifetime, protected pairing, tray, notification/widget, signed update and session-side-app requirements must each have implementation and actual platform evidence before desktop completion is claimed.

### Local native connection
The main window alone receives `local-bootstrap`. The host locates the bundled sibling `delidev` binary, runs explicit `server start` with the exact app/development origins, then `device pair-local` and `device inspect` against its fixed private `desktop-client` subdirectory. Go owns singleton startup, compatibility, detachment, private filesystem validation and all pairing mutations. The native host independently checks the returned metadata before reading that one credential file; only the separate revocable client credential enters the trusted renderer. It never returns the owner token or permits renderer-selected executable/argv/filesystem paths.

The initial local UI accepts the fixed `http://127.0.0.1:46310` listener; a different live listener is preserved and requires explicit compatible-client guidance. The root defaults to the platform user configuration directory plus `delidev`; an explicit absolute native process `--data-dir` selects another scope. CLI controller output is limited to 64 KiB per stream and 40 seconds, both streams are joined, and process failures expose stable classifications only. Ambient credentials are excluded from the child environment. Client exit leaves the Go server alive. Remote selection, capped automatic supervision and explicit-stop coordination remain required future increments.

Navigation is restricted to the app entry document, including Tauri’s empty custom-scheme path. External navigations and popups are denied. CSP permits only the fixed local RPC and native IPC; only the development policy permits the fixed development HMR connection and inline styles. The app webview is ephemeral. The source-owned vector icon has an 8-bit RGBA PNG and Windows ICO export.

## Storage
The server remains the only database owner. Frontend query caches and unsent drafts are memory-only and scoped to the selected connection. No credential, prompt, transcript, cursor or account browser state enters Web Storage. Client exit cannot stop server-owned sessions. Native profiles and server startup are separate infrastructure boundaries.

## Security
Only trusted app content receives native capabilities. Renderer/server calls require exact allowed origins and the explicitly selected connection. Account credentials and GitHub PATs must never enter read responses. Never expose a shell, arbitrary executable/file reader, network proxy, or secret-bearing diagnostic object to the renderer.

## Logging
Expose typed safe problems and correlation IDs, plus independent connection/retry state. Never log input, resource documents, tokens, native output or account locators. Native logs use stable operation/failure classifications.

## Build and Test
Frontend changes require package-local `pnpm test`, typechecking and production Rsbuild verification. Component tests use real generated Connect router transports and explicit test credentials, exercising draft preservation, revision/request identity, authentication failure and inert content. Native Rust work additionally requires root Cargo tests and host-specific lifecycle checks. Generated `dist` is removed from the final worktree.

`pnpm build:native` generates the typed client, frontend and target-specific Go sidecar before building the native host. `pnpm dev:desktop --data-dir /absolute/private/scope` runs the embedded frontend in that host. `pnpm prepare:sidecar [TARGET_TRIPLE]` accepts only the six explicit macOS/Windows/Linux x64/arm64 target mappings and never silently substitutes the host. Native packaging/signing/publication remain separate acceptance work.

## Dependencies and Integrations
Use the repository's React, Connect Query, React Query and Rsbuild pins. The Go server/Worker and canonical versioned protobuf remain authoritative. Toss frontend guidelines inform explicit status, focused forms, clear action hierarchy and accessible components.

## Change Triggers
Update app/root AGENTS, project ownership, the client/protocol contracts, build/CI configuration and evidence ledger with path, interface, lifecycle, security or supported-platform changes. Never conflate a component fixture or build with native packaged acceptance.

## References
- [Project](project-delidev.md)
- [Requirements](cmds-delidev-requirements.md)
- [TypeScript client](packages-delidev-api-client-contract.md)
- [Evidence](cmds-delidev-evidence.md)
- [Repository defaults](repository-defaults.md)
