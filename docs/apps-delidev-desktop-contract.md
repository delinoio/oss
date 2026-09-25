# DeliDev desktop client

## Scope
`apps/delidev` owns the React desktop presentation and native Tauri host for DeliDev. The full desktop requirements in issue #964 remain normative; implemented surfaces and remaining native/product work are recorded in the evidence ledger.

## Runtime and Language
React 19.2.8 and TypeScript render the trusted app through Rsbuild. The native Tauri component implements the window, bounded Go sidecar startup, private client bootstrap and local-server supervision. Go owns every product operation. The frontend development origin is fixed at `http://127.0.0.1:46311`; conflicts fail. This is a desktop application, not a browser product or deployed website.

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

The initial local UI accepts the fixed `http://127.0.0.1:46310` listener; a different live listener is preserved and requires explicit compatible-client guidance. The root defaults to the platform user configuration directory plus `delidev`; an explicit absolute native process `--data-dir` selects another scope. CLI controller output is limited to 64 KiB per stream and 40 seconds, both streams are joined, and process failures expose stable classifications only. Ambient credentials are excluded from the child environment. Client exit leaves the Go server alive. Remote profile selection remains required future work.

One native supervision loop per desktop process invokes Go's `server ensure`; Go serializes cross-process controllers and checks durable local intent plus the original listener/TLS/origin configuration before launching. Healthy and stopped scopes are checked every five seconds; transient failures use exponential equal-jitter delays capped at 30 seconds, and incompatible/invalid states are inspected at most once per minute without replacing them. An unconfigured or explicitly stopped scope never starts automatically. The read-only `local_server_status` capability exposes only closed state, attempts, bounded delay and failure classification. Native logs report state transitions, never child output. Full app exit wakes and joins the loop and cancels any short controller; it does not stop the detached server.

The desktop offers explicit server stop through direct authenticated Connect with retained mutation identity and a description of its effect on clients/Workers. Accepted stop, automatic-restart suppression and session cleanup remain distinct. Explicit local start uses the existing closed native bootstrap command. Renderer connection verification retries at most three transient status reads and never repeats a native startup/pairing operation or product mutation automatically. Reconnection to the same server/device/endpoint/credential revalidates active read queries without replacing drafts or pending mutation identities; an identity change still clears connection-owned state.

Navigation is restricted to the app entry document, including Tauri’s empty custom-scheme path. External navigations and popups are denied. CSP permits only the fixed local RPC and native IPC; only the development policy permits the fixed development HMR connection and inline styles. The app webview is ephemeral. The source-owned vector icon has an 8-bit RGBA PNG and Windows ICO export.

## Storage
The server remains the only database owner. Frontend query caches and unsent drafts are memory-only and scoped to the selected connection. No credential, prompt, transcript, cursor or account browser state enters Web Storage. Client exit cannot stop server-owned sessions. Native profiles and server startup are separate infrastructure boundaries.

Generated Connect Query read keys contain their read request parameters, including search text and pagination cursors, only in the connection-scoped memory cache. Credentials and mutation payloads never enter those keys. Retain at most eight inactive query pages in addition to the bounded currently observed pages; cancel and clear the entire cache when its connection is disposed, including React Strict Mode effect replay. Conversation navigation retains at most 100 previous cursors and always offers a return to the first page.

### Interactive requests and input queue
Session and inbox views expose the same generated interaction RPCs. Questions retain every native ID, exact offered labels, optional free text and an explicitly chosen unanswered array. Bound the complete answer before retaining it. Secret-marked questions disable ordinary response submission until protected delivery is implemented. Reading an inbox item remains independent of answering or approving it; queued delivery is shown separately from native acceptance and closure.

The pinned Codex 0.151.0 approval forms preserve command decision objects and their exact offered amendments. File approvals use the native closed decision enum. Permission forms start with no grant, preserve path/glob/special descriptors and native deny rules, allow a requested write to be reduced to read, and distinguish turn/session duration. A present native entries array overrides legacy path mirrors even when empty. Unknown native versions cannot submit invented choices. The Go server remains the authority for request validity and first-response-wins concurrency.

Queue pages expose edit, remove and explicit Steer for unclaimed items. Editing captures the original revision and preserves the draft after a peer change while blocking stale submission. Steer binds the selected queue item and observed active execution/turn; it cannot fall back to ordinary send. Every uncertain action retains its immutable request for explicit retry, including after another client closes the original request. Refreshing a failed event stream resnapshots without clearing the composer or retained mutation identities.

### Editable settings and account connection
Provider, model, AI account, Agent Worker and instruction-template forms use generated configuration RPCs. Provider presets seed concrete editable values; custom API endpoints and explicit keyless authentication remain server-validated. Model provider/native identity is immutable when editing. Agents retain ordered weighted account links, ordered templates, routing inheritance and native permission/options. Configured compatibility does not establish native execution capability. Project/repository forms are implemented; local execution-Worker registration and lifecycle remain separate required work.

Each edit captures its original resource revision and full document. Server-owned account observations and model discovery provenance are preserved. A peer revision change blocks a new save while retaining the draft; an uncertain save retries only its original request. Closing and reopening Settings preserves an edit. Forms cap complete documents at 1 MiB and instruction content at 128 KiB UTF-8 before retention, and selectors retain one bounded page with an explicit selected identity outside that page.

Account metadata editing is separate from credential connection, validation, catalog refresh and disconnection. API-key input is password-masked, limited to the server's printable ASCII contract, and cleared on submission or hiding the form. Its wire bytes can remain only in the bounded connection-scoped exact pending request after uncertainty; completed React Query mutation variables are released. Keys never enter read keys, logs or browser persistence. Keyless connection requires the selected provider's explicit keyless configuration. Subscription configuration never implies implemented login or inherited system credentials.

Connection starts unverified. Validation and model discovery show their independent server observations; only explicit actions invoke them. Disconnection describes cancellation and credential removal, and presents accepted state separately from pending credential/Worker cleanup. A retained removal marker retries its original request ID and expected revision, never the newer account revision. Unsupported or unsafe revision encoding disables that retry instead of guessing.

### Projects, repositories and configuration actions
Project forms retain ordered repositories and an explicitly selected primary repository. Removing the primary clears that choice without selecting a replacement. Agent/account restrictions preserve the difference between unrestricted selection and an explicitly configured empty list, which permits none.

Repository forms request read-only inspection from the explicitly selected Worker, show its canonical root and recorded remote defaults, and add only that inspected checkout. Base and starting references remain separate explicit local-branch/remote-branch/commit choices; omitted choices use the Worker's inspected default or return an ambiguity error. Fetch remains an explicit Worktree preparation preference. The renderer never normalizes another machine's paths or runs Git. Active/uncertain inspection prevents discarding or submitting its containing edit while the modal can still close without losing state.

Saving repository configuration acknowledges a durable asynchronous job. The UI retains and polls that exact job, shows queued/claimed/uncertain/failed/canceled/succeeded independently, and reports configuration publication only after success. Failed/canceled saves permit an explicit return to the retained draft; uncertainty cannot submit another save. Read refresh never resubmits a Worker operation. Confirmed configuration deletion uses the captured revision, preserves retained sessions and explains affected future schedules/account cleanup; uncertain retries retain their original identity. Agent routing previews show the server's candidates, eligibility, selected account, nullable quota evidence and fallback without consuming routing state or granting execution readiness.

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
