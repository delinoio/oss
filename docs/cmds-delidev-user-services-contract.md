# DeliDev current-user service contract

## Scope
Issue #1086 adds optional native server and Worker registrations. Go owns `cmds/delidev-cli/internal/userservice`, the CLI infrastructure boundary, and authenticated `SystemService` operations. This is separate from Worker job, workspace and native harness process ownership. The complete product requirements remain in [requirements](cmds-delidev-requirements.md).

## Runtime and Language
The installed Go `delidev` executable is registered directly. macOS uses the current user's GUI LaunchAgents domain; Linux uses the existing `systemd --user` manager; Windows uses Task Scheduler with the current SID, `InteractiveToken`, `LeastPrivilege` and `IgnoreNew`. No system service, password, elevation, pre-login execution, linger enablement or logout-survival guarantee is introduced.

## Users and Operators
Local control requires an existing private owner server scope or a separate already-paired Worker scope. Installation neither initializes a database nor pairs a device. Owner and currently authorized paired-client Connect callers may control the server computer's server and its fixed local `<server-scope>/worker`; Worker credentials cannot invoke these product operations. Remote Workers use their local CLI infrastructure boundary, not a server-selected remote filesystem path.

## Interfaces and Contracts
- `delidev server service install|status|start|stop|remove [--revision N]`.
- `delidev worker service install|status|start|stop|remove [--worker-dir PATH --revision N]`.
- `delidev service-control install|status|start|stop|remove --kind server|worker [--revision N]` uses authenticated Connect on the selected server.
- Installation of a server can additionally retain `--listen`, `--tls-cert`, `--tls-key` and `--allowed-origins` references, with the same validation as explicit startup. The RPC installer retains the running server's original configuration. Defaults remain loopback; conflicts never remap a port.
- `SystemService.GetUserService` and `ControlUserService` use closed kind/action/state enums, canonical UUID-v7 request and installation IDs, exact uint64 revisions, current-state receipt reads and ordinary correlation metadata. `USER_SERVICES_V1` identifies protocol support, not availability of a GUI domain, user bus or interactive session.

A stable registration name binds current user, canonical private scope and process kind. Installation creates a stopped registration without replacing an existing native definition. Repeated installation preserves the original installation identity. Start persists running intent, enables the login trigger and starts the original executable. Foreground, detached and service entry points use the existing server/Worker scope locks; Start refuses a competing foreground owner. A service controller has its own exclusive runtime lock as well. Automatic `server ensure` may reuse a live authenticated server, but an installed native registration prevents it from launching a competing detached supervisor; stopped service intent suppresses unavailable-server recovery. Joined server-service Stop also carries stopped intent into the original ordinary lifecycle generation, so removing its registration cannot erase suppression; a newer foreground generation is never overwritten.

Stop commits durable stopped intent before disabling the login trigger. Every native or delayed login launch checks that private intent before entering the existing product lifecycle. Local Stop waits up to 20 seconds for joined service-controller completion, native-manager PID absence and the original product lock's release. RPC Stop returns bounded acceptance separately from shutdown, allowing the server to acknowledge its own Stop. The observer waits for the bounded native-disable attempt to settle before canceling the original controller; an interrupted attempt has a fixed 30-second grace. Neither pending nor uncertain cleanup is reported as stopped successfully. A receipt replay reads current state and cannot stop a replacement start.

Remove requires confirmed service-controller cleanup, disables login startup and removes only the verified original native registration. It preserves the database, owner/device authentication, Worker jobs, workspaces, execution journals, logs and service receipt history. Repeated Remove confirms absence without repeating native writes. Reinstallation obtains a fresh installation ID and keeps prior runtime evidence. Installing or removing an inactive registration does not stop an independently running foreground process.

Ordinary server/Worker Stop remains authoritative. The service wrapper observes the retained product stopped intent after joined exit and suppresses its own login registration. A later login does not turn product Stop into explicit Start. A new service Start may explicitly reopen product lifecycle intent; automatic relaunch requires its original compatible running configuration. Missing or failed controller-completion evidence remains recovery-required and blocks service replacement; absence of a PID or release of a lock is insufficient.

## Storage
Private per-kind atomic JSON records retain the installation's canonical scope identity, executable file identity/SHA-256, user identity and safe configuration references, exact revision, desired state, and up to 1,024 original request claims with matching metadata-only events. A single synchronized replacement publishes intent/event/receipt acceptance atomically. State and control locks serialize bounded publication and native writes. Completed receipts cannot repeat native effects; incomplete claims preserve uncertainty, including after client loss. A separate per-installation runtime record retains the original process birth and UUID-v7 instance/start identities, with positive completion published only after the product controller joins. Removed registrations retain all historical records.

Every status observation, including startup polling and pre-control validation, holds the state gate against new controller admission. It probes the runtime lock before reading the completion record and retains a successfully acquired lock through observation. This orders the record read after the original controller's completion publication and prevents observation from claiming a runtime lock needed by a concurrently admitted controller. After joining the product controller and intent observer, completion publication also holds the state gate, excluding transient Windows permission-inspection handles from atomic replacement while retaining runtime ownership until the positive record is durable. This bounded publication must survive cancellation of the already-joined controller. An active lock keeps shutdown pending; a free lock with incomplete completion evidence remains uncertain.

Native definitions contain executable/scope/installation references only. macOS and Linux publish definitions exclusively from synchronized temporary files. Removal atomically claims the original Unix definition without replacement and verifies the original file identity and complete bytes before unlink; changed claims remain preserved for recovery. Windows retains the original exclusively created task's canonical exported XML in private state and compares its entire graph except the separately controlled Enabled setting.

## Security
Before native control, independently validate the stored scope/executable identities and bytes, user, complete native definition, and any live manager PID against the private launch record, current executable, user and exact process birth. Never signal a PID directly or adopt an unknown definition. Linux additionally rejects drop-ins and validates loaded executable argv, fragment, user, restart and kill properties through typed D-Bus values. It accepts one absolute filesystem-backed Unix bus path, including standard percent escapes and optional 16-byte GUID metadata, and rejects remote/abstract/autolaunch or multiple-address fallback. It uses only an existing same-user Unix user-bus socket and passes only required user-bus/runtime selectors to systemctl; no bus autolaunch or inherited product credentials. Windows COM operations are constant PowerShell source with dynamic values supplied as bounded UTF-8 JSON on stdin; creation uses CREATE, never replacement, and no password. Bind a running task engine to the original action's kernel parent and then independently verify the action's process birth/user/executable.

Windows process image observation resolves the Win32 path with the same canonicalization used during installation before comparing the original executable path and file identity. An 8.3 spelling of that same file remains compatible; a different path, changed file identity, SID or process birth remains recovery-required. Native path normalization never replaces independent ownership evidence.

`launchctl print` is an unstable debugging interface. The adapter recognizes only a complete exact path/program/argv/PID ownership profile, including inactive loaded definitions; unknown or ambiguous formatting fails closed. The single kickstart wait allows 15 seconds for launchd's ten-second restart throttle; timeout retains uncertainty without another launch attempt. It never unloads a job merely to discover which cached definition it had. Current GUI-domain availability is checked separately, and only the documented service-not-found outcome establishes absence.

A missing native user session leaves positively owned Stop suppression durable but returns unavailable without native control or shutdown claims. Native writes are bounded and cancellable. An unknown or failed native outcome retains the original claim; reconnect cannot blindly replay it. Authorization is rechecked after bounded admission/native observation immediately before intent publication and again before each native side effect, so a client revoked during inspection cannot publish Stop suppression. Product RPCs return only service kind/ID/revision/state, desired state, login enablement and independent controller-cleanup confirmation; no executable, private path, PID, birth identity or credential is exposed. Service completion never implies native session cleanup or grants Resume.

## Logging
Structured `slog` diagnostics include service kind, stable action/stage/state, request/installation IDs and typed safe error codes. The service entry point appends to an identity-checked owner-only per-kind log in its private scope, including macOS and Windows where native default output would otherwise be discarded. Native stderr, full definitions, environment, commands, credentials, prompt/output and private paths are excluded from service control diagnostics.

## Build and Test
Run root `go test -race ./cmds/delidev-cli/...` and `go vet ./cmds/delidev-cli/...`, `pnpm proto:check`, and the DeliDev API client's lint/test/build commands. Required generated bindings are tool-owned. Remove ignored generated `dist` directories from the final worktree.

Deterministic controlled-native fixtures cover repeat install/start/stop/remove, exact receipt replay after a newer start, stale revisions, delayed login after Stop, joined cleanup barriers, foreign definitions/PIDs, replaced executable/scope identity, foreground exclusivity, cancellation, revoked authority and unconfirmed native writes. Real Connect/SQLite checks verify actor authorization, typed metadata, correlation, revision conflicts, receipt-only replay and redaction. The TypeScript client reads absent service metadata without invoking a real service manager.

`DELIDEV_NATIVE_USER_SERVICE_TEST=1 go test -race ./cmds/delidev-cli/internal/cli -run '^TestNativeUserServiceLifecycle$' -v` explicitly selects a real native fixture using temporary server/Worker scopes and uniquely named current-user registrations. It preserves authentication/data, verifies Worker attachment, self-Stop acknowledgment and controller cleanup, refuses a competing foreground server and a delayed stopped launch, and repeats server startup/removal. Native Windows/Linux execution, real logout/relogin, platform distribution and account acceptance remain separate evidence requirements; cross-compilation is not native evidence. See the [evidence ledger](cmds-delidev-evidence.md) for actual results.

## Dependencies and Integrations
Reuse the existing protected-file locks, process-birth observation primitives, foreground server/Worker controllers, authenticated Connect transport, Go/TypeScript generators and existing godbus dependency. No dependency or SQLite schema migration is added.

## Change Triggers
Update this document, the project and CLI/protocol/client contracts, evidence ledger, and scoped AGENTS whenever registration ownership, authorization, native definitions, intent/receipt semantics, cleanup proof, command shape or platform support changes.

## References
- [Microsoft process image observation](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-queryfullprocessimagenamew)
- [Microsoft short filename conversion](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getshortpathnamew)
- [Go Windows path canonicalization](https://go.dev/src/path/filepath/symlink_windows.go)
- [Project](project-delidev.md)
- [CLI/server/Worker](cmds-delidev-contract.md)
- [Owned process contract](cmds-delidev-process-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Repository defaults](repository-defaults.md)
- [Apple LaunchAgents](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)
- [systemd service properties](https://www.freedesktop.org/software/systemd/man/latest/org.freedesktop.systemd1.html)
- [D-Bus address specification](https://dbus.freedesktop.org/doc/dbus-specification.html#addresses)
- [Task Scheduler logon trigger XML](https://learn.microsoft.com/en-us/windows/win32/taskschd/logon-trigger-example--xml-)
- [Task Scheduler exclusive registration](https://learn.microsoft.com/en-us/windows/win32/taskschd/taskfolder-registertask)
