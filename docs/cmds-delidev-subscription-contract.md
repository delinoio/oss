# DeliDev native subscriptions

## Direct execution startup

[Direct startup](cmds-delidev-execution-startup-contract.md) removes manual installation inspection and separate execution probes from new negotiated assignments. Initialize the actual selected process and validate its protocol, credential mode and settings before input. Optional observed version metadata cannot grant or deny execution by numeric comparison. Existing protected generation, original account lease, server-owned OAuth and credential write-back/cleanup remain required; readiness never grants login, account refresh or credential ownership.

## Failed subscription cleanup reservations

System `FAILED_SUBSCRIPTION_CLEANUP_V1 = 41` and all batch/status/result declarations were reserved on main by the originating change before activation. System 38 belongs to Claude and 39/40 to Grok. The reservation itself granted no cleanup, login or deletion authority.

One explicit owner/paired-client `CleanupFailedSubscriptions(request_id)` admits a server-owned durable job and freezes every candidate account ID, revision, original initial ChatGPT server LOGIN and deletion request UUID in account child jobs. Receipt replay binds the original actor and server and returns the same current job. Only one batch can be pending per server. Admission scans the complete bounded server inventory; frontend account pages never select work. Failed, canceled, expired, unsupported and interrupted initial logins qualify, including credential-cleanup-confirmed failures. The same explicit batch also selects disconnected subscription Accounts for every supported service, including metadata-only accounts and completed logout accounts. Version-2 private child jobs distinguish original failed LOGIN cleanup from disconnected configuration deletion; version-1 children retain their original LOGIN-only meaning. Disconnected targets have no invented operation ID and must retain no active login, native process, generation, identity commitment, active Worker ownership, lease, recovery, removal or active observation. A completed Worker logout may retain its historical owner-machine routing hint; it grants no active authority without a generation, lease, pending action or recovery, and does not bypass protected-reference checks. Connected accounts, restored generations and independent Worker/native/observation ownership are excluded.

The joined server controller applies the original 30-second account attempt and account gate. It rechecks the original requester, exact account revision and login ownership. It shares the existing native/credential cleanup and configuration deletion checks, including protected vault references and complete Agent/Project/retained Session references. Its native and credential checkpoints advance the child's expected revision in the same transaction. Changed settings or ownership remain retained. Failed attempts are terminal retained outcomes with closed reasons, never automatic retries. A later deliberate button click can create a fresh batch. An explicit `DeleteConfiguration` can admit one failed initial ChatGPT LOGIN through the same controller under the original deletion request ID. The child freezes that requester, account, confirmed revision, LOGIN and deletion command; cleanup checkpoints advance only its effective revision. The final deletion receipt retains the original public revision and request bytes. Before that receipt exists, mutation and replay use the immutable child under the existing job parent index to reserve the public request ID for that exact deletion command; admission rejects previously used IDs transactionally. Accepted single-account work survives client departure; terminal failures require fresh observation and explicit confirmation, and receipt/status reads never retry cleanup.

Confirmed deletion, tombstone/browser obligations, the existing configuration receipt, child result and aggregate counts commit together. Restart reads only original jobs and confirmed cleanup checkpoints, never login or callbacks. An interrupted attempt without a native checkpoint remains retained; confirmed native cleanup may resume protected cleanup. Shutdown cancels and joins the controller before store/vault closure. Revocation permits server-owned retained-result bookkeeping only, never substituted account deletion authority. Restore blocks queued/pending cleanup jobs and quarantines historical nonterminal jobs and receipts, so restored work cannot regain deletion authority.

`GetFailedSubscriptionCleanup(job_id, page_token)` returns current revision/state, total/processed/deleted/retained counts and at most 50 original account results. Signed cursors bind the actor and batch. Result metadata contains only the original alias, account ID, closed outcome/reason and safe problem code. Logs contain job/operation IDs, counts, phases and safe codes. Existing generic jobs/receipts need no migration or Rust/native change.

## Native Claude subscriptions

The originating change established System `CLAUDE_SUBSCRIPTIONS_V1 = 38`, Worker
`NATIVE_CLAUDE_SUBSCRIPTIONS_V1 = 20` and the complete protocol allocation
closure on main at `8a698d04d51f9db3a041b909edf85a7811f09ff3` before feature
implementation. The server advertises 38 for the existing SubscriptionService's
Claude branch. An original Worker advertises 20 only after verifying installed
Claude Code `2.1.236` through an isolated empty profile and confirmed cleanup.
A reserved number or ordinary installation observation never grants this lane.

An owner or authorized paired client explicitly selects a local or remote
Runner Device for login. `claude auth login --claudeai`, `auth status` and
`auth logout` run on that Worker with an account-specific private
`CLAUDE_CONFIG_DIR` and matching secure-storage directory. DeliDev reconstructs
the native environment; personal profiles, imported/external tokens,
Console/API login and API relay authentication are excluded. The original CLI
owns OAuth exchange, credentials and automatic renewal. DeliDev does not read
native authentication files or distribute their bytes to the server or another
machine. Claude quota/credit inspection and redemption remain excluded.

Server-owned optional Account JSON retains an opaque native profile UUID,
original owner machine, keyed identity commitment, authentication generation
and original operation/lease metadata. The Worker computes the commitment from
bounded native identity under its private server-scoped key. The original login
also pins that commitment in its private profile owner record; fresh native
status must match before reauthentication completion or execution input. A
missing pin or changed account retains the ownership fence. Raw identity stays
local. Authentication success requires the original login process to exit,
joined owned descendants and `auth status` proving first-party `claude.ai`
subscription authentication with no API-key source. Reauthentication reuses the
same owned profile and identity, creates a new generation and preserves the
connection ID. A different native identity cannot replace that account.

One exclusive account lease serializes login, reauthentication, execution and
logout. Login/reauthentication cannot enter behind another lease. Logout may
queue behind execution after atomically revoking readiness and canceling that
execution. Execution checks the service/model/harness, dedicated capability,
original owner Runner, profile and generation before consuming an input. Wrong
Runner, old Worker and API-authentication selections fail without a fallback.
Existing native permission, model, effort, continuation, history, usage,
publication and Stop checks remain required. Native subscription execution has
no API proxy path and injects no API key or relay token into the CLI.

`SubmitSubscriptionLoginCode` is an owner/client write-only operation bound to
the original actor, account revision, operation and live native login. It
accepts one bounded visible-ASCII readline input only for the native
browser-code method. The server records the original request claim before
retaining bytes in memory. `TakeSubscriptionLoginCode` permits only the original
Worker/device/instance/lease and records consumption before releasing bytes
once. Exact submit receipt replay never replenishes input. Lost memory, a lost
Take response or canceled/expired/revoked ownership never permits another code
submission or native launch. Neither code nor original browser URL enters jobs,
receipts, resources, synchronized state, shared caches or logs.

Progress exposes only the original initiating principal's live native URL,
closed login method and safe diagnostic metadata. URL validation pins the
original CLI's HTTPS host/path, client, PKCE and subscription scopes; DeliDev
never reconstructs or repairs the URL. Native browser presentation binds the
original trusted desktop window, server/lifetime, operation and exact URL.
Explicit reopening uses that same binding. Claude presentation creates no
DeliDev callback listener or code-exchange authority.

Cancellation wins before successful connection publication. A claimed operation
remains fenced until its original process joins and native logout/status plus
owned-profile removal are confirmed. Restart, offline/replaced Worker,
uncertain delivery and failed cleanup retain the original operation/lease and
recovery ownership. Reconnect does not repeat Take, native login or code input.
The original Worker may reconcile its durable original process journal and
report cleanup-only recovery under the retained lease; that path cannot publish
a login success or authorize execution. Closed success receipts remain
immutable even if their response is lost.

Logout runs the official CLI, independently proves disconnected status and then
removes only the canonical, inode-bound owned profile under the existing
2,048-file/64 MiB cleanup bound. Oversized, symlinked, replaced or uncertain
profiles retain recovery; they are not reported as removed. Account deletion
requires confirmed native logout/profile cleanup and no retained owner,
generation, lease or operation. Session checkpoints retain a digest-bound
profile reference and independently bounded native history, never authentication
files. Restored ownership metadata is quarantined and cannot adopt another
Worker's credentials. Portable configuration contains no native ownership.
No SQLite migration is added; real migrations 1–31 and reserved baseline 32
remain unchanged.

Fixture, build and browser-layout results do not establish actual account login
or packaged macOS/Windows/Linux acceptance. Each platform requires real original
CLI login, multiple-account isolation, first/follow-up input, Stop/Resume,
logout/deletion and original offline/restart/cleanup evidence before support is
reported complete. Keep the outstanding matrix visible in PR/CI evidence.
Follow the desktop, protocol, harness, storage and structure contracts.

## Scope

The feature implements dedicated Codex subscription login, refresh, execution and logout in `cmds/delidev-cli`, `protos/delidev/v1/subscription.proto` and the generated DeliDev clients. The server owns authorization, encrypted credentials, generations and exclusive account leases. An explicitly selected paired Worker owns execution processes and its private authentication files. The independently negotiated server login lane owns browser login, authentication refresh and logout without a Worker. The complete product requirements remain in [the feature's snapshot](cmds-delidev-requirements.md).

Existing-login import, externally supplied token bundles, internal-only `chatgptAuthTokens` and concurrent use of one managed Codex bundle are excluded. Claude follows its independently negotiated device-owned lane above. Desktop login controls and native-owner quota/reset-credit operations are implemented together in the subscription lifecycle feature PR. Full native recovery and real-account/platform acceptance remain separately identified; fixtures cannot establish them. This implementation does not complete the feature or claim a release.

## Runtime and Language

Go owns server and Worker business logic. The native profile pins installed Codex `0.151.0`; DeliDev never installs it. The managed profile uses the official app-server protocol, a fresh private `CODEX_HOME`, file-backed native authentication and the built-in OpenAI provider. API execution and discovery retain their existing ephemeral credential profile. Independent native Fork uses the API profile or the separately negotiated protected managed profile for this feature in the Fork contract. Managed ChatGPT Sidechat uses its separately negotiated protected Fork lease under the Sidechat contract. Source inspection cannot grant credentials; the exact claimed Sidechat job owns protected Take, joined native/plaintext cleanup and original-account write-back before publication.

## Users and Operators

The server owner or an authorized paired client initiates lifecycle operations. A paired Worker advertises `managed-codex-subscriptions-v1` only after an empty-home native configuration handshake and owned cleanup; the server echoes this closed capability. Execution and explicit-machine lifecycle operations select an execution machine. Independent server lifecycle operations omit that selection. Workers cannot initiate owner lifecycle operations or read another machine's bundle.

Execution admission requires that selected Worker's negotiated managed capability before claiming a session or consuming queued input. Ordinary native installation discovery alone cannot admit subscription execution; an unsupported Worker leaves the original input queued without selecting a fallback.

## Interfaces and Contracts

Subscription accounts use schema 2 with required closed `subscription_service: "chatgpt" | "claude" | "grok"` and no `provider_id`. Native models use schema 2 with `source_kind: "subscription"`, the same service and no API Provider. Services map exactly to Codex, Claude Code and Grok Build; a model's single compatible harness must match its service. API accounts/models keep their existing schema-1 provider identity and forbid subscription fields. Neither service metadata nor model registration grants native login, quota or execution support.

The historical provider-bound shape used `native-subscription`, `subscription` authentication, an empty endpoint and optional `subscription_harness: "codex"`. Migration 28 retires every legacy subscription account and those providers/models without inferring a service from any field or display name. That shape is retained only as read-only historical metadata under the [storage contract](cmds-delidev-storage-contract.md#subscription-retirement). New native-subscription Providers and edits that replace service/account/model identity are rejected.

Managed Codex lifecycle and execution select an independent ChatGPT account. Go rechecks its original generation/connection/lease and matching immutable service model at every authority boundary without loading an API Provider. Publication-only registrations expose no API relay path; independent service identity does not grant an upstream API operation. Immutable snapshots and new native response/diagnostic records carry the selected service instead of a fabricated Provider ID. Historical provider-bound snapshots and usage retain original bytes and attribution; retired selections cannot resume or silently reroute.

`SubscriptionService.RequestSubscription` accepts login, refresh and logout with a UUID-v7 mutation receipt and current account revision. An explicit machine preserves the original Worker lane; omitted `machine_id` selects the independently negotiated server lane. `CancelSubscription` cancels the original pending login. `GetSubscriptionProgress` returns short-lived browser/device presentation data only to the original initiating principal. CLI equivalents are:

```sh
delidev account login --id ID --revision N --machine-id ID --device-code
delidev account login-progress --id ID --operation-id ID
delidev account cancel-login --id ID --revision N
delidev account refresh --id ID --revision N --machine-id ID
delidev account logout --id ID --revision N --machine-id ID
```

Omitting `--device-code` selects native browser login. Ordinary acceptance/status output contains references and state, never a login bundle. The explicit progress command presents the native one-time URL/code without persisting or logging them. Cancellation may race native login completion, but its durable state wins before connection publication.

Revoking a paired initiating client atomically cancels its still-queued login, refresh and logout operations. This grants no lease, changes no credential generation, and preserves logout's accepted execution revocation and any independently granted lease. A fresh authorized request may replace a canceled queued operation. Successful execution write-back may retain the newest protected generation but cannot restore readiness after accepted logout revocation; a fresh authorized logout must complete cleanup. Claimed operations retain their original ownership and recovery obligations; revocation never proves native cleanup.

An independently authorized outbound `WatchSubscription` lane supports four concurrent account operations per Worker. `TakeSubscription`, `PublishSubscriptionProgress` and `FinishSubscription` are restricted to the selected current Worker/device/instance. Bundles travel only in the protected Take/Finish messages, with a 64 KiB bound; they never enter jobs, execution output, events or ordinary resource responses. Take receipt replay never redistributes a bundle or authorizes another native launch.

One durable account lease spans login, refresh, execution or logout. Refresh/logout requests can wait behind an execution lease. Other sessions retain their selected account and wait after a definite busy refusal; unknown delivery is never retried. Other accounts acquire independent leases. Native execution uses an immutable subscription selector and the existing publication/history/workspace machinery. Its publication registration exposes no API proxy path and cannot obtain an API relay lease.

A lifecycle Take that races another lease grant retries only a definite busy refusal, using the exact original claim, account, operation and revision. This wait preserves the shared Worker connection and the execution that won ownership; caller cancellation joins it. Unknown delivery still closes the ownership lane and cannot be retried. The lifecycle revision remains its original positive account observation, not a requirement that later cleanup or metadata edits leave the account unchanged. Take rejects future observations and independently requires the same uncanceled queued operation, action, selected machine and currently authorized initiating actor; cancellation, replacement and recovery cannot gain authority from an old observation. Owner lifecycle requests still require the current account revision, and execution Take still requires its exact claimed job revision.

A fresh lifecycle Take returns `Canceled` with exact `ErrorDetail.cause = subscription_take_not_admitted` only from a rolled-back admission transaction after current account, Worker, instance, installation and update fences when the queued original is absent, replaced or canceled. It grants no lease, bundle or native authority. Replay, claimed/recovery ownership, wrong action/machine, transport cancellation and post-commit errors never supply this proof. The Worker recognizes both the code and exact cause, retires only that original private claim, and leaves independent account operations on the shared lane running without Finish, retry or native launch. Every unproven refusal retains the existing lane and recovery fences. Claude classifies a queued replacement after the original account, current Worker instance, verified installation, update, revision, action and recovery/lease fences, before reading the replacement-owned native profile or owner machine. The stale claim cannot adopt, cancel or change that replacement. A matching operation still requires its original native profile and selected machine; a canceled initial login with a retired profile retains its exact original canceled-operation proof.

A confirmed pre-native execution failure returns the byte-identical unused original bundle over protected Finish with cleanup true and success/refresh false. This includes a failed Open only when owned process closure and unchanged-file cleanup are independently confirmed. The server compares the returned bytes with the immutable vault generation before releasing the lease, preserving its generation, connection, health and any independently queued refresh/logout. Cleanup alone, missing bytes or changed authentication cannot establish this outcome; uncertain execution/refresh retains blocked original vault material and recovery ownership. Explicit logout retains its separately authorized credential-removal semantics.

If an atomic authentication write fails before publishing its destination and before native ownership, confirmed cleanup instead requires the original private home, synchronized and rechecked destination absence, and the complete bounded retained-file credential scan. A leftover temporary credential file, a missing/foreign home or an uncertain observation retains the fence. Reject every retained entry in the atomic writer's `.pending-` namespace, including empty or partial files and nested entries, independently of token matching. Published authentication follows the unchanged original-byte comparison/removal path; native-owned cleanup cannot substitute file absence for its captured-bundle proof.

The lease pins operation, account generation, Worker machine/device/instance, server epoch and original lease revision. Completion checks that original revision while preserving later metadata edits and cancellation. The connection ID remains stable across bundle rotations. The final native bundle is saved under a new immutable generation, old protected references are tombstoned, and independently confirmed native/file cleanup precedes another grant.

Read-only Doctor inspection follows the [ChatGPT saved-storage adapter](cmds-delidev-diagnostics-contract.md#chatgpt-saved-storage-adapter). Only a valid idle service-native ChatGPT account can read its exact saved `AccountLogin` generation. Pending lifecycle, leases, active/native server work, active or uncertain quota/reset-credit work, removal, recovery and unresolved cleanup exclude the read. Completed history alone does not exclude it. Request-local selectors fence same-connection rotations and changed eligibility at report publication. Readability grants no authentication, native cleanup, recovery or retry authority; native-only Claude and unactivated Grok remain unsupported. This adapter never changes subscription state or protected storage.

Finish's ownership-error guard ends after the ownership mutation and final required vault cleanup commit, before its response-resource observation. A canceled or failed presentation read cannot add a recovery fence to settled state. Failed final cleanup still fences ownership, and accepted receipt replay preserves that fence without repeating vault work.

Successful `account/read` does not prove refresh. Both Worker and server require changed token material, a strictly newer native `last_refresh` value and unchanged account/user identity. Server identity commitments enforce one managed owner for the same provider account/user across account aliases. Local logout proves native/file removal and server vault cleanup; provider-wide revocation remains best-effort and is never reported as confirmed.

The pinned native login-completed envelope includes nullable `onboardingEntrypoint` metadata. Accept null or the pinned closed `life_sciences` value without launching another onboarding flow or treating presentation metadata as authentication evidence; reject unknown values while still requiring the original login identity and successful completion.


## Server browser login and account naming

System `SERVER_SUBSCRIPTION_LOGIN_V1 = 30` activates the shared reservations established on main by the originating change. It is independent of service-account inventory capability 17 and Worker capabilities. An authenticated owner or paired client can omit `machine_id` for ChatGPT login, authentication refresh or logout. The server must have the verified Codex 0.151.0 installation. No Worker registration, startup or selection is required. Claude requires the explicit Runner lane under capability 38; Grok remains unsupported. API authentication, execution selection and existing quota authority do not change.

Go records the original actor, operation, server epoch, bounded lifetime, finish identity and closed progress state in optional server-owned `server_operation` metadata. Accepted request replay returns the original receipt without another native launch. Claiming requires the original queued pending operation and no Worker credential lease; `native_started` is an exclusive credential fence. An existing execution lease retains ownership while queued refresh/logout waits. Cancellation is serialized with final publication. Successful native completion, joined process closure, final-file comparison, private-runtime removal, vault staging and old-reference cleanup precede confirmed success. Uncertain cleanup and previous-epoch operations retain recovery ownership and cannot relaunch or redistribute credentials. Optional JSON metadata requires no database migration and does not alter migrations 26–30.

### Failed initial server login cleanup

A failed, canceled or interrupted initial ChatGPT server login can release its account for explicit configuration deletion after independently confirmed cleanup. This applies only to the original server login with no connection, credential generation, identity commitment, Worker lease, native machine owner, quota/reset-credit operation or account removal. Refresh, logout, connected accounts, restored credential generations and Worker ownership retain their separate recovery boundaries. Cleanup never repeats authentication or callback forwarding and never claims provider-wide revocation.

The optional server-owned `server_operation.cleanup_phase` uses the closed values `native-confirmed` and `credentials-confirmed`; omission preserves legacy metadata and means no checkpoint. Joined native closure and synchronized private-runtime removal establish the first phase. The pre-native path can also establish it when the opener never ran. Removing every account-owned `account-login` protected reference precedes the second phase. Only that final transaction clears pending/native/recovery fences and sets disconnected health. Preserve the original operation, failed/canceled/expired/unsupported outcome, safe diagnostic and current configuration preferences. A canceled server context alone cannot retain recovery after both cleanup phases are confirmed.

A later explicitly accepted server or Worker login supersedes this completed server attempt. Worker admission retires only its fully confirmed terminal server metadata before creating the new independent pending operation; an unsettled checkpoint never permits replacement.

Server maintenance makes one 30-second cleanup attempt per original operation in each server process, using the existing shared 16-account lane and account gate. It joins these children at shutdown and excludes a still-running original login. A retry after server restart resumes its durable native checkpoint without another native login. Failed attempts keep their original ownership and do not run again on every 250-millisecond scan.

Legacy claimed logins without a checkpoint require the original private process owner index and valid journals. Independently held native controller locks block recovery; a released controller permits the existing platform ownership reconciler to confirm or stop only that original process tree. Retain the index after journal retirement. Missing individual journals, foreign ownership and file/PID absence alone grant no cleanup proof. After process confirmation, remove only identity-checked original auth and discovery-probe directories, rejecting symlinks and preserving other operations. Legacy metadata with `native_started=false` proves either no native claim or previously joined cleanup; unexpected retained auth/probe files contradict that proof and remain blocked.

Vault, filesystem, authorization or state-publication failure retains the configuration and cleanup checkpoint for a later server process. The Settings deletion screen continues to require a fresh account read and explicit confirmation; cleanup never deletes configuration. Existing revision, retained-reference and independently observed browser-profile cleanup checks remain authoritative. Logs contain only original operation references, cleanup phases and stable codes. Tests use temporary synthetic accounts and native process fixtures; they do not establish installed-app, real-account or all-platform acceptance.

Browser login uses the ordinary `account/login/start` ChatGPT profile, never automatic device-code fallback. The pinned app-server disables its own browser opening; the trusted native desktop opens the returned original address once. Exact native binding replay cannot open another browser. Deliberate reopening uses only that same operation/address. Local desktop login retains the original installed Codex callback. Remote desktop login binds its registered loopback callback on both address families and forwards one bounded code/state/scope query through authenticated `ForwardSubscriptionCallback` to the original server's Codex callback. The callback requires the original state, actor, current server epoch, operation and trusted window/opening lifetime. Duplicate, foreign, expired, canceled and late callbacks are refused. Go durably claims forwarding before HTTP, inherits no proxy, follows no redirects and never resends uncertain delivery or performs another exchange.

Browser callbacks accept exactly `http://localhost:1457/auth/callback` (Codex 0.151.0) or `http://127.0.0.1:1457/auth/callback` (Codex 0.159.2). Preserve the original URL, state and callback authority through browser binding and forwarding; the two spellings cannot replace one another within an accepted operation. Forward only to fixed IPv4 loopback and retain the original callback HTTP Host. Remote native receivers retain both loopback listeners and require that original Host. Other hosts, numeric IPv4 spellings, ports, paths and duplicate query fields are rejected. URL rejection logs contain only the fixed failure classification and original operation reference. This compatibility rule does not recover prior failed accounts or establish actual account/platform acceptance.

Codex 0.151.0 can send `/cancel` to an occupied preferred port 1455 before using its registered fallback 1457. The server first owns a private 1455 listener, refuses a conflict, and retains it through joined native closure. This prevents cancellation of another login and lets the original Codex listener choose its registered 1457 callback. A fallback conflict fails explicitly. Remove this bounded workaround only when the pinned native protocol provides an exclusively owned callback port without foreign cancellation. The remote desktop never remaps its callback or substitutes device authentication.

Typed progress separates preparing, waiting, succeeded, canceled, expired, unsupported, failed and recovery-required. Successful login exposes its exact current generation and one transient name suggestion, chosen from email, provided display name, then ChatGPT within the existing 256-byte UTF-8 alias bound. Native login success remains the authentication evidence; JWT identity claims only project consistent identity. Suggestions belong to the original actor/operation/generation, expire with its lifetime and remain only in memory until the user explicitly saves a name. They never enter resources, receipts or logs. Refresh/logout cannot suggest names.

Settings Add account is an explicit event that creates a default service-named account and immediately requests browser login. Mount, Strict Mode, polling and reconnect cannot repeat creation/login. Only confirmed original success advances to editable name entry. Save uses the current revision and changes only the alias while preserving server-owned bytes; a conflict retains the draft for a fresh explicit save. Later and navigation retain accepted default-name metadata and authentication. Navigation discards the Settings presentation/native callback receiver and rejects late continuations without business cancellation. Only Cancel login requests cancellation.

### Project requirements

- Failed subscription cleanup follows `cmds-delidev-subscription-contract.md#failed-subscription-cleanup-reservations` after main reservation the originating change (System 41). Preserve server-owned complete batches, original actor/login/revision, shared vault/reference deletion checks, atomic result/tombstone receipts, joined shutdown and restore quarantine. Terminal failures require a new explicit batch; status/restart never relaunches login or callbacks. No database migration or Rust/native change.

- The feature server quota uses System 46 and the separate optional server quota owner under `cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota--issue-1728`. The owner explicitly permits allocation and activation in one complete batch PR without the prior main merge under the shared allocation workflow; existing allocation ownership and migration dependencies remain unchanged. Preserve original actor/epoch/connection/generation, once-only claims, confirmed native/file/reference cleanup, joined shutdown and strict Worker/reset-credit ownership. No new RPC, Worker capability, migration or Rust/native change. Real-account/installed-native/remote/platform acceptance is owner-performed and nonblocking for this batch; fixtures establish no such acceptance.

- Server quota V2 follows the feature and `cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota-v2--issue-1854`: record and activate System 50 in the complete feature PR, preserving 18/46/49. Omitted-machine manual/batch/maintenance quota uses a separate server access-token-only native profile even during Execute. Refuse token refresh, retain captured protected references through rotation, and keep quota cleanup uncertainty independent of execution leases. Preserve explicit Worker/reset-credit semantics, lifecycle/deletion/restore fences and joined shutdown; no RPC, Worker allocation or migration. Real native/account/remote/platform acceptance is owner-assigned and nonblocking for this batch.

### cmds/delidev-cli/internal/cli constraints

- `account list` accepts optional closed `--account-type api|subscription` and `--provider-id UUID` selectors through the list-only Connect request. Preserve the unfiltered default for existing callers; filters are applied before SQL pagination and bound to cursors. Snapshots and event streams must not inherit them.

- Generic JSON configuration selects schema 2 only for service-native subscription accounts/models or retired Agent repair, preserving API schema 1 and the version-1 result envelope. Subscription list requests never combine a Provider selector. Follow the independent subscription identity and portable bundle contracts.

- Subscription login/refresh/logout may omit --machine-id only after capability 30 negotiation. Explicit machine selection retains the Worker lane; no CLI account command implicitly starts server/Worker. Device-code auth remains an explicit CLI alternative, never browser fallback. Dedicated original login-progress output may show transient original state/name/generation; operational logs must not contain it.

- Server-owned ChatGPT reset credits follow the feature under the subscription contract. System 49 separately negotiates omitted-machine consumption through the settled original server credential generation; active executions and explicit machines retain the Worker lane. Preserve immutable actor/epoch/connection/generation/inventory/key/selection, once-only durable sends, explicit same-key reconciliation after confirmed native/file cleanup, independent quota outcomes, cross-domain ownership fences and joined shutdown. No new RPC, Worker/entity allocation or migration. Keep fixture/build checks separate from real native/account/platform acceptance.

### cmds/delidev-cli/internal/domain constraints

- Optional Account.subscription state contains only server-owned generation references, identity commitments and actor/lease fences. Configuration cannot manufacture or replace it; historical accounts omit it unchanged. Keep closed action/phase/capability values under `cmds-delidev-subscription-contract.md`.

- A restored managed subscription may retain a valid historical generation without a connection only while recovery-required. This is quarantined evidence; ordinary usable generations still require subscription-authenticated connections. Follow the storage and subscription contracts.

- Keep service-native subscription identity closed to ChatGPT/Claude/Grok and its exact harness; OpenCode Go is the separate closed key-backed OpenCode identity under its owning contract. API Provider identity remains separate. Match complete account/model identity in routing and immutable snapshots; retired Agents require explicit reconfiguration before resolution.

- Native subscription observations preserve sparse quota fields, authoritative credit counts and original generation-bound operation keys under the subscription contract. Only fresh positive native evidence may clear exhaustion; typed failure/reset outcomes cannot grant recovery.

- Subscription server_operation is optional server-owned closed metadata. A machine-less pending claim is valid only for its exact server operation/actor; native_started requires no Worker lease and the original generation. Active/recovery ownership retains pending authority. Terminal metadata grants no credential use. Keep APIs, immutable historical service attribution and real migrations unchanged.

- Claude subscription optional ownership pairs native profile and owner machine, with original operation/actor/epoch/lifetime, once-only code claim/consumption and closed safe diagnostics. Usable generations require native identity commitment and subscription connection. Metadata grants no credentials; preserve account/model/harness matching, historical identity and no migration under the subscription/storage contracts.

- The feature reference pricing uses validated exact ModelIdentity (one API Provider UUID or SubscriptionService plus native ID). Collector rates are exact bounded decimals; missing and zero remain distinct. Identity/reference metadata grants no account or execution support. Follow the catalog, usage and network contracts.

- Paid-credit observations retain original ChatGPT quota ownership under System 76 / Worker 51. Project only bounded per-bucket flags, nullable exact decimal strings and original successful timestamps; omit sparse balances, reject malformed/reflected values and preserve last-good evidence. Capability omission grants no support, generation replacement clears balances, and configuration saves grant no observation or spending authority. Follow the subscription contract; no migration.

### cmds/delidev-cli/internal/harness/claude constraints

- Original installed Claude Code 2.1.236 owns subscription OAuth and automatic renewal through private account-specific configuration and secure storage. Use official auth login --claudeai, auth status and auth logout; never inspect auth files, personal profiles or external tokens.

- Validate the original closed CLI URL without reconstruction and accept one bounded readline approval input. Join the original login process and descendants before independent subscription status confirmation. Clear URL/code/native identity buffers after their bounded use; retain only safe structured diagnostics.

- Keep native subscription initialization distinct from API/discovery authentication. Reconstruct environment paths; no API key, relay bearer/base URL or host-managed/imported auth enters subscription execution. Existing permission/model/effort, original input/history/continuation and Stop proofs remain mandatory.

### cmds/delidev-cli/internal/harness/codex constraints

- Accept the installed Codex 0.159.2 observation fields in thread Start/Resume/read, settings notifications, empty agent-message questions and quota/response-usage metadata under the harness contract. Discard private observations without replacing immutable effective settings, enabling plugins/environments/Daybreak or inferring billing cost. Populated questions remain a private extension. Keep unknown-field rejection, original history and managed-account recovery fences. Event failure logs use closed validation stages, never raw methods or payloads.

- Follow the parent and repository instructions. Managed authentication is a separate explicitly leased profile under `cmds-delidev-subscription-contract.md`. Require native login completion, file-backed ChatGPT authentication and the official built-in provider; reject inherited overrides, API keys and external tokens. Successful account/read alone never proves refresh. API execution/discovery retain their ephemeral profile.

- Discovery and subscription lifecycle processes pin `features.plugins=false` before launch and verify disabled plugins through the complete bounded normalized native feature inventory before granting a client. Missing, enabled, malformed or incomplete observations reject the profile and join the original native owner. Thread execution retains its independent feature policy. Keep disposable runtime cleanup bounds unchanged; disabling downloads cannot recover prior failed accounts or establish real-account acceptance.

- Private quota reads accept optional nullable `ordinaryUsageAllowed` booleans, `accountId` strings and opaque `rateLimitUpsell` JSON from the official Codex response. Discard these fields before projection; they grant no identity, routing, recovery or consumption authority. Preserve strict unknown-field, typed-field, duplicate-key and envelope-bound checks.

- Pinned native quota/reset-credit adapters omit unit parameters where the original protocol requires it, project bounded sparse metadata and preserve the official idempotency key and closed outcomes. Exclude display/billing fields and credential reflection; native updated notifications cannot grant authentication or execution authority.

- Server quota V2 follows the feature and `cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota-v2--issue-1854`: record and activate System 50 in the complete feature PR, preserving 18/46/49. Omitted-machine manual/batch/maintenance quota uses a separate server access-token-only native profile even during Execute. Require and discard only the original external-token login’s closed success completion with no managed login ID, error or onboarding payload before reading quota; this metadata grants no renewal authority. Refuse token refresh, retain captured protected references through rotation, and keep quota cleanup uncertainty independent of execution leases. Preserve explicit Worker/reset-credit semantics, lifecycle/deletion/restore fences and joined shutdown; no RPC, Worker allocation or migration. Real native/account/remote/platform acceptance is owner-assigned and nonblocking for this batch.

- Quota privacy checks reject exact original bucket and reset-credit IDs against protected raw values and complete Base64 forms at every length. Strip only the adapter-owned final window suffix; retain long substring checks without short-word substring rejection. Worker reads/updates and server-owned quota reads share `ValidateQuotaSecrets` before publication under the subscription contract.

### cmds/delidev-cli/internal/knownmodels constraints

- Never read subscription credentials, run harnesses, create resources or infer account/readiness authority. Log only catalog version/date and stable failure codes. Fixtures use isolated temporary state and injected transports.

### cmds/delidev-cli/internal/server constraints

- Subscription cleanup includes fully disconnected service-native accounts without synthetic login IDs. Explicit failed initial ChatGPT deletion uses the shared durable cleanup controller, preserves the original public deletion revision/receipt while checkpoint revisions advance, and never retries a terminal attempt without fresh explicit confirmation. Retain original actor/native/vault/reference checks; no new RPC, protocol allocation or migration. Follow the subscription and account contracts.

- Managed Codex subscriptions follow `cmds-delidev-subscription-contract.md`. Keep explicit provider selection, a separately authorized Worker lane, one durable per-account login/refresh/execution/logout lease, original actor/revision/generation fences and immutable latest-bundle write-back. Never redistribute a Take receipt or uncertain generation, expose bundles in records/logs, grant API relay authority from subscription registration, or delete unresolved ownership.

- Require the selected Worker's negotiated managed Codex capability before claiming subscription execution or consuming its queued input. Ordinary native installation discovery does not establish managed authentication support.

- Initiator revocation must atomically settle its still-queued subscription lifecycle operations without granting a lease or changing credential generations. Preserve accepted logout revocation, independent active leases and all claimed/native ownership; only a fresh authorized request may replace the canceled queued operation. Successful native write-back must preserve logout-revoked health until a fresh authorized logout completes cleanup.

- Lost subscription ownership must reject progress reads and publication and purge the in-memory URL/device-code presentation under the account gate. Retain the original pending operation, lease and recovery evidence; cache invalidation never proves native cleanup or grants a retry.

- Recovery-required subscription ownership rejects every new Finish before vault staging/deletion and at the final transaction. An already accepted Finish receipt may replay read-only without changing a later retained lease, pending operation or recovery fence.

- Quota/reset-credit capabilities 18/19 and Worker observation capability 8 follow the subscription contract. Queue complete server-side refresh batches, persist original send claims before native work and retain uncertain consumption for explicit same-key reconciliation. Publish account recovery and its deduplicated account-scoped Inbox record atomically; never grant a second active credential writer.

- Quota maintenance checks due eligibility read-only before mutation and rechecks it transactionally. If every candidate was concurrently queued, roll back the empty batch; do not emit recurring no-op receipts or change signals.

- Joined successful native-owner cleanup settles unpublished quota reads as failed while preserving last observed values and future read/execution admission. Retain original-key uncertainty only for possible reset-credit consumption.

- Independent subscription capability 30 admits omitted-machine login/refresh/logout under the subscription contract. Original actor/epoch/request/lifetime and native_started metadata form a disjoint exclusive credential fence; Worker leases cannot coexist. Reuse verified isolated Codex, joined process/file cleanup and protected generations. Claim callback forwarding durably before fixed loopback HTTP, reject foreign/duplicate/late state and never retry uncertain delivery. Restart retains recovery without relaunch. Suggestions stay original-success-bound and memory-only until explicit name Save; logs retain only opaque IDs/actions/codes.

- Subscription browser callbacks permit only the exact registered localhost and 127.0.0.1 port-1457 `/auth/callback` addresses under the subscription contract. Retain the original URL/state/Host; forwarding always connects to fixed IPv4 loopback and never substitutes one accepted spelling for another. Reject duplicate query fields and malformed state. Log URL rejection with only its fixed classification and original operation reference. Preserve prior recovery fences.

- Server subscription failures retain actual Codex version and the first safe native failure separately from cleanup/recovery. Login logs use original operation correlation without account identifiers. Publish diagnostics only to the original authenticated actor/operation and preserve uncertain ownership without resending work. Follow the subscription and protocol contracts.

- Subscription cleanup failures log only closed native-process/auth-file/runtime stage and reason enums, the original operation reference and bounded inventory counters/limits. Preserve the public recovery classification, auth-file comparison, original process joins, directory identity, symlink refusal and existing 2,048-file/64 MiB limits. Runtime diagnostics contain no paths, native output, file content or credentials; they cannot clear existing recovery fences.

- Claude subscription business logic belongs in claude_subscriptions.go under System 38. Bind original owner/client operation and Worker/device/instance/lease, claim codes before memory retention and consumption before once-only release. Idle progress/code polling is read-only. Cancel/revocation/expiry wins before connection publication; wrong Runner fails before input consumption. Retain uncertain ownership and admit original cleanup-only recovery without login/code replay. No Claude bundles, credential files, raw identity, URL/code persistence or quota inference.

- `desktop_credentials.go` owns read-only startup Keychain checking for app-owned macOS servers. Page current connected API accounts, resolve immutable selected profiles, skip keyless/disconnected/removal/subscription ownership, and read exact current references. OAuth reads bypass refresh/exchange/cleanup. Clear returned buffers even on failure, recheck original client authorization, retain original attempt on observation and join canceled native work before vault closure. Only explicit predecessor-bound Retry may replace terminal failure; never change account health/revisions/receipts or send provider HTTP. Follow the desktop/credential contracts.

- The feature active Execute quota admission uses only the original initialized native process under its exact account/connection/generation/lease/revision/epoch/Worker/device/instance and initiating-actor proofs. Explicit, omitted-machine, Refresh all and maintenance share eligibility without saved discovery/version gates. Preserve strict idle quota and reset-credit/reconciliation discovery checks; registry absence grants no new native owner. Follow the subscription contract; no allocation or migration.

- Server quota V2 follows the feature and `cmds-delidev-subscription-contract.md#server-owned-chatgpt-quota-v2--issue-1854`: record and activate System 50 in the complete feature PR, preserving 18/46/49. Omitted-machine manual/batch/maintenance quota uses a separate server access-token-only native profile even during Execute. Refuse token refresh, retain captured protected references through rotation until confirmed native cleanup and checked exact obsolete-reference retirement before terminal publication, and keep quota cleanup uncertainty independent of execution leases. Preserve explicit Worker/reset-credit semantics, lifecycle/deletion/restore fences and joined shutdown; no RPC, Worker allocation or migration. Real native/account/remote/platform acceptance is owner-assigned and nonblocking for this batch.

- Server subscription maintenance initializes the shared vault and reads the exact original `AccountLogin` generation under `accountGate`, sharing API account synchronization and cancellation. Release the gate before native work; retain retryable open failures and the original operation/cleanup owner. Follow the credential contract.

A fresh subscription lifecycle Take may return Canceled with exact cause `subscription_take_not_admitted` only inside its rolled-back admission transaction after original account/Worker/instance/installation/update fences when the queued original is absent, replaced or canceled. Replay, claimed/recovery state, wrong action/machine and post-commit errors never carry this proof. No lease, bundle or native authority is granted. Claude rejects a stale queued replacement claim after original current-Worker/update/revision/action and recovery/lease fences, before replacement-owned profile/machine checks. Keep matching-operation profile/machine validation and the original canceled retired-profile proof; never adopt or cancel the replacement.

- Automatic reset credits use actor/connection/generation-bound typed consent and atomic durable episode admission under System 77. Require the original failed Execute job/native thread/turn and active Worker 52 credential lease; reject idle adoption, free-text authority and restart replay. Logout/generation replacement clear consent. Preserve original same-key explicit reconciliation and cleanup; follow the subscription contract.

### cmds/delidev-cli/internal/store constraints

- A single-account subscription cleanup child reserves its original public configuration deletion request/revision before the final receipt exists. Both mutation and replay check that immutable command through the existing job parent index; admission rejects already used public IDs transactionally. Preserve ordinary batch and restored-job semantics; no migration. Follow the subscription/account contracts.

- The feature reserves migration 28 for service-native subscription identity and legacy configuration retirement. Keep versions 26 and 27 in their existing order and implement both real predecessors before activating 28. Reservations use one original `pr` or owning `issue`; retain that provenance after an implementation PR exists. The planned reset is backup-first and atomic, preserves historical bytes/attribution and configured-empty deny-all restrictions, and refuses unsettled native ownership or cleanup without inferring a service.

- Migration 28 follows real 26/27, backs up first and atomically retires original provider-bound subscriptions without inference. Preserve original record bytes in history-only storage and forbid live Get/admission fallback. Keep tombstones/receipt redaction, configured-empty deny-all, surviving ordered weights and affected Schedule reset. Refuse unsettled original native/protected/browser ownership; only a private historical restore candidate has the explicit no-current-authority migration exemption.

- Worker update admission and idle checks run under the claim transaction, including original terminal/forward/subscription/deletion ownership. Filter matching pending/signed scopes before applying query bounds. Preserve current installation metadata across managed restore, and never release uncertain update fences through history pagination or restored receipts.

### cmds/delidev-cli/internal/worker constraints

- OpenCode event subscriptions must precede the original input claim and consume the owned native connected record before sending. Preserve arrival order and original JSON event IDs without treating them as SSE replay cursors. Retain bounded private native bodies for separate typed observers; transport recognition grants no completion, interaction, usage or publication authority. Do not ignore unknown frames, silently evict identity history or infer EOF/idle as completed work. Reopen only through the harness contract's once-only same-process native event reconciliation: verify original process/authentication/effective configuration, retain all input/reply/publication claims, consume listener readiness before bounded repeated reads, and publish no partial result on uncertainty. Keep private read proofs separate from native event identities; only positive original reply evidence can resolve delivery. Stop, Archive, revocation and owned closure must cancel/join the cycle, and recovered terminal/history/cleanup remain separate boundaries. Stream failure blocks new input and cancels pending mutations without releasing their durable claims; retain prior valid observations and join the reader/watchdog on closure.

- The internal OpenCode owned API interface must retain original initializer settings and claims, validate input before consuming subscription authority, and serialize event dequeue with typed observation. Canceling only a waiting consumer must leave the original stream, queued arrivals and observer usable; actual subscription loss, owner cancellation, EOF and bounded-capacity failures still require reconciliation. Bind an unobserved retained in-memory input only to its original stream/root for inspection or cleanup, never as restart/replay authority. Keep native interruption, owned Stop intent, explicit recovery and generic joined Close separate; closure alone cannot publish successful input or synthesize a Stop receipt. Real Worker/native claim integration with fixture relay authority does not prove public dispatch, account registration or publication.

- Managed Codex subscriptions use only the selected installed Codex 0.151.0 and a fresh private file-authentication profile under the protected lease. Journal original claims before native work, never retry uncertain delivery, verify native identity and independent last_refresh/token changes, join native cleanup and save the final bundle before another grant. Accept all four Codex permission modes for API and subscription execution; default omits the sandbox override and full-access may expose managed authentication files to native tools. Reject non-canonical or workspace-overlapping `CODEX_HOME` paths before writing `auth.json`; preserve original identity, cleanup and recovery proofs without claiming unrestricted same-user isolation. Keep recovery evidence and synthetic test credentials isolated; follow `cmds-delidev-subscription-contract.md`.

- Codex thread publication and checkpoint retention/read must compare the exact provider selected by the immutable accepted authentication profile: built-in OpenAI for managed subscriptions and the existing relay provider for API execution. Copy private checkpoint comparison flags only from that accepted configuration; do not infer a profile from a native observation or widen uncertain-lease recovery.

- Close and join the managed subscription lane after uncertain delivery/completion so the server retains lost leases as recovery-required. A durably acknowledged operation failure must not interrupt unrelated accounts on that lane.

- Quota/credit observation dispatch shares the original active Codex process through a joined account-owner registry. Idle observations use one short exclusive lease. Preserve official operation keys after uncertainty; quota/read and credit/consume do not perform inference or substitute for authentication refresh. Join registry users before closing the native process.

- Idle quota/reset-credit observation publication failure does not itself imply credential-owner recovery. Collect the unchanged valid native bundle and independently join native/file cleanup before completing the short credential lease; retain original operation uncertainty/key for explicit credit reconciliation. Failed bundle/native/file validation still fences ownership.

- Claude subscription Worker 20 requires verified original Claude Code 2.1.236 and joined empty-profile cleanup. Persist original lease/process/code claims before native effects; use one canonical private account profile and scoped secure storage, original-machine owner and exclusive lock. Official login/status/logout owns auth; raw identity stays local and the original keyed commitment is pinned in private profile ownership before later status comparison or execution input. Original recovery joins process journals and confirms logout/status plus bounded owned-profile removal before reporting cleanup. Execution reuses stream-json/history with no API auth injection or auth-file checkpoints. Never replay uncertain Take/login/code or claim actual platform acceptance from fixtures.

Subscription lifecycle Take treats only Canceled with exact cause `subscription_take_not_admitted` as an operation-local no-grant refusal. Retire its original private claim without Finish, retry or native launch; preserve other accounts on the joined lane. Execute, ordinary Canceled, RecoveryRequired, unknown delivery and uncertain Finish retain existing lane/recovery fences.

- Automatic reset credits use Worker 52 only with verified managed Codex. Publish the closed failed-turn marker after original execution progress, claim and consume through the same retained native lease, and fence/join its observer before native cleanup. No idle process, restart, Sidechat or Fork may adopt automatic spending. Preserve explicit reconciliation and original uncertainty under the subscription contract.

## Storage

Optional server-owned `Account.subscription` JSON preserves historical account bytes when absent and adds no destructive schema migration. It retains only generation references, keyed identity commitments, pending-operation metadata, original actor and lease fences. Configuration writes cannot manufacture or change this state. Unresolved ownership prevents configuration deletion. Managed database restore also refuses pending, leased or recovery-required ownership. Restored subscription references remain disconnected and recovery-required; historical generations and claims remain evidence, while the external vault is unchanged and supplies no restored authority. Clear an allocated subscription state with no generation, identity, pending operation, lease or recovery fence; settled logout or failed login owns no external reference to quarantine.

The existing OS-backed [credential vault](cmds-delidev-credentials-contract.md) stores bundles with `account-login` purpose and immutable UUID-v7 references. Native plaintext exists only in the exclusive server or Worker private runtime; it is removed after owned process closure. Execution installs credential cleanup at the owned auth-file write boundary, before publisher setup and registration, for both current and predecessor runtime selections. Failures before native ownership remove and synchronize the original auth file; a failed native Open permits removal only when its process cleanup is independently confirmed. Uncertain native ownership retains the protected lease and cannot authorize optimistic deletion. Execution retains original history and checks its bounded remaining files for credential remnants. Worker journals contain only original claim/completion identities and cleanup outcomes, never bundle bytes or token digests. Temporary login presentation lives only in server memory.

Uncertain delivery or completion closes the Worker subscription lane and joins its children so the server records lost ownership. An acknowledged failed operation retains its confirmed completion and leaves unrelated accounts on that lane active. Missing refresh write-back, unconfirmed cleanup, Worker loss/replacement or server restart retains the original lease as recovery-required. The old generation cannot be redistributed. Recovery publication purges the in-memory login presentation under the account gate while retaining the original lease and pending operation. Progress reads reject recovery-required or previous-epoch ownership, and the lost Worker cannot republish a URL or device code. These lifecycle commands neither erase recovery evidence nor clear an uncertain lease; independent native recovery remains a separate product boundary.

## Security

Disposable subscription lifecycle processes disable native plugins before launch and verify the normalized disabled feature through the harness profile. This prevents unrelated startup/account-change catalog downloads from overflowing cleanup inventory. Discovery uses the same download restriction; actual session execution retains independent feature authority. Keep the original 2,048-file and 64 MiB runtime cleanup limits, identity checks, symlink refusal, original process joins and final auth-file comparison. Disabling downloads grants no recovery of prior failed logins, credential adoption, automatic retry or authentication authority.

Uncertain protected execution completion escapes ordinary job-error reporting and closes the primary work lane after joined cleanup. The original started job claim and protected completion journal remain retained; the server records the lost execution lease as recovery-required. Workspace cleanup failures preserve this completion uncertainty, and receipt replay cannot manufacture a successful write-back. A successful protected Finish RPC can acknowledge a fenced account; it permits ordinary execution reporting only after final bundle capture and independent cleanup, or the separately verified unused-original pre-native outcome. Acknowledgment alone cannot replace the started job claim.

Once subscription ownership is recovery-required, every new Finish is denied before vault staging or deletion and again at its final transaction. Only an already accepted receipt may replay through this handler; that read-only replay preserves any later retained lease, pending operation and recovery fence. A late background completion cannot replace the independent recovery path.

Remote transport retains authenticated TLS; loopback development retains the existing protected local RPC boundary. Authorization is rechecked after vault I/O and at publication, including the original initiating client's revocation. Managed login starts with no existing authentication file and requires the original native login-completed observation. Bundle validation rejects API keys, external auth modes, unknown credential fields, foreign identities, symlinks and oversized files. JWT claim comparisons are identity consistency checks, not signature verification or substitutes for native OAuth completion.

Login confirmation, refresh and logout share one strict native `account/read` response profile. Codex 0.159.2 serializes the known optional `workspaceRouting` field even with `experimentalApi: false`. Omission and null remain valid for earlier/native disconnected responses. A present object requires the exact original bundle's `chatgptAccountId`, a nonempty account ID of at most 24 KiB without credential-field whitespace/control separators, an HTTPS origin of at most 8,192 bytes without user information, path, query or fragment, and the closed `accountRoutingOverride` enum `NO_CONSTRAINT`, `us` or `us_cr`. An explicit origin port must be nonempty decimal in the unsigned 16-bit range. Discard this metadata after validation; it cannot select Go endpoints, supply credentials, grant authentication or enter resources, receipts or logs. Continue rejecting unknown and duplicate fields at every object depth. Logout requires explicit `account: null`, required OpenAI authentication, absent/null routing and independently absent native `auth.json`. The original native login completion and independently changed file/token evidence for refresh remain mandatory; compatibility never clears an existing recovery fence.

The merged native configuration must retain file storage, ChatGPT login and the built-in OpenAI provider without inherited endpoint, command authentication, bearer, query or header overrides. Native execution never inherits unrelated system logins. Private files do not promise an OS sandbox against unrestricted same-user access. Managed execution accepts `default`, `read-only`, `workspace-write` and `full-access` for both API and subscription accounts. Default leaves native sandbox selection unset; full-access permits native tools to access managed authentication files. Keep the fresh `CODEX_HOME` outside every accepted workspace root after canonical-path checks and reject overlapping/aliased paths before writing `auth.json`. These location and cleanup checks do not promise isolation from unrestricted native tools. All tests use isolated temporary state and synthetic credentials.

Both native thread start and resume recheck the merged configuration for the actual execution workspace before sending the mutation. A successful startup-directory handshake cannot authorize a workspace-specific provider or authentication override.

Thread publication and checkpoint retention/read also bind the native provider to the immutable accepted authentication profile. Managed execution requires built-in OpenAI; API execution retains its existing relay provider. The private checkpoint comparison copies the profile only from accepted configuration, preserves API checkpoint bytes and cannot promote an uncertain subscription lease into recovery authority.

Managed execution captures native identity and the final authentication bundle once before closing the native wire, including before the normal terminal Close. Earlier exits perform the same bounded read before deferred closure, with no automatic retry. After native/process closure, credential cleanup requires the remaining auth file to match those captured bytes, scans retained history and only then submits protected write-back. A terminal event alone establishes none of these cleanup or authentication facts.

Credential cleanup scans retained native files for raw token material and padded or unpadded standard/URL Base64 copies under the existing file/count/byte bounds. Finding a remnant leaves cleanup unconfirmed and retains recovery ownership without erasing the original native history.

Ordinary Codex closure fences new protocol writes, closes stdin on the retained original process and allows at most three seconds for native EOF shutdown before forced cancellation. Join the original process controller and any input-close operation before returning. Codex's temporary helper aliases require normal native destructor cleanup; forced cancellation can leave them behind. Protocol failure, caller cancellation and expired grace retain immediate original-owner termination. The subsequent authentication scan still refuses symlinks and credential remnants. A graceful exit is process evidence only: it cannot release an existing recovery fence, change a protected completion receipt or establish bundle/file cleanup by itself. Closed structured shutdown logs distinguish timeout and joined cleanup without paths or native content.

## Logging

Use structured `slog` events for accepted operations, grants, completion, capability availability and recovery failures. Log only opaque account/operation/lease/machine identities, closed action, cleanup classification and stable error code. Native protocol messages, token bundles, JWT identities, URLs, device codes and filesystem paths never enter these logs.

Managed native failure logs additionally retain the closed internal `stage`: `login-start`, `login-completion`, `login-cancel`, `account-read`, `bundle-validation` or `local-logout`. The stage identifies the first failed check within its existing safe phase and original operation correlation. It does not change public diagnostic schemas, retain native field values or authorize retry.

Server cleanup failures additionally emit `server_subscription_cleanup_failed` with the original operation reference, closed stage/reason and bounded `file_count`, `observed_bytes`, `file_limit` and `byte_limit` counters. Stages distinguish `native-process`, `auth-file`, `runtime-validation`, `runtime-inventory`, `runtime-removal` and `runtime-sync`. Reasons distinguish unconfirmed native cleanup, failed protected reads, bundle mismatch, filesystem/identity failure, symlink/non-regular entries and file/byte limit overflow. Inventory counters stop at the first failure; an individual file size above the byte limit is capped at limit plus one before accumulation to prevent overflow. No path, filesystem error text, native output, content, identity or credential is retained. Internal typed runtime failures preserve the original public recovery error and cannot alter cleanup proof, account state or retry authority.

## Build and Test

Run `go test -race ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, `pnpm proto:check`, and the API client's tests/typecheck. Generate bindings through pinned root Buf tooling. Controlled native-process fixtures cover browser/device progress, completion, cancellation, file rotation, unchanged refresh evidence, logout and symlink refusal. Real loopback Connect/SQLite fixtures cover lease races, protected-channel authorization, generation fencing, lost write-back, cancellation, identity uniqueness, independent accounts, API relay denial and secret-free outputs/database files.

These fixtures do not authenticate real accounts, execute hosted inference or establish installed-Codex/desktop/Windows/Linux/release acceptance. Record actual executed checks, source revisions and unresolved limits in the feature, its pull requests and CI logs/artifacts.

Lifecycle fixtures verify normalized plugin disablement before any login request, rejection of incomplete observations, unchanged thread feature policy and sequential independent account login without changing the first saved configuration or protected generation. Cleanup fixtures preserve exact file/byte limits, identity/symlink refusal, auth-file equality and secret-free failure counters. The opt-in installed empty-home logout smoke waits for asynchronous startup work, rejects plugin cache creation and checks joined private-runtime removal after native closure. It uses no browser, real account credentials, OAuth completion or inference.

Account-response fixtures cover omitted/null/validated routing across browser/device completion, refresh and logout, malformed/foreign metadata, unknown/duplicate fields and secret-free stage logs. The explicit `DELIDEV_NATIVE_INITIALIZE_EXECUTABLE` empty-home logout smoke exercises an installed Codex's actual `account/read` response and joined process/private-runtime cleanup without opening a browser, authenticating a user or invoking inference. This check cannot establish real OAuth completion or packaged-platform acceptance.

## Dependencies and Integrations

Reuse authenticated Connect, the server vault, current Worker discovery, owned process supervision and existing Codex session/history publication. No provider brokerage, native retry emulation, account/model failover or new dependency is added.

## Change Triggers

Update the account/harness/session/protocol/client contracts and affected scoped `AGENTS.md` owners when the native profile, public operations, generations or recovery boundaries change. Record implementation status and validation in the feature, its pull requests and CI logs/artifacts; do not add repository evidence documents. Update the project index only for ownership, domain catalog or cross-domain invariant changes.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [Project index](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Credential storage](cmds-delidev-credentials-contract.md)
- [Native harnesses](cmds-delidev-harness-contract.md)
- [Sessions](cmds-delidev-sessions-contract.md)
- [Wire contract](protos-delidev-v1-contract.md)
- [Pinned official account protocol](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server-protocol/src/protocol/v2/account.rs)
- [Pinned native authentication storage](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/login/src/auth/storage.rs)
- [Codex 0.159.2 account response profile](https://github.com/openai/codex/blob/ff6aec96948b70d94983af2641a6b67c94faeff5/codex-rs/app-server-protocol/src/protocol/v2/account.rs)


## Native quota and reset credits
System capabilities 18 (`SUBSCRIPTION_QUOTA_V1`) and 19
(`SUBSCRIPTION_RESET_CREDITS_V1`) negotiate the two product operations separately.
Worker capability 8 retains the verified managed Codex profile for fresh idle native-owner admission. Active original Execute quota reads use the initialized execution protocol as described below. The
pinned upstream source is commit `78c290807ce710180111df227df3b7a4fe845452`.
`account/rateLimits/read` omits its unit parameters; the native wire encoder's
explicit `OmittedParams` profile preserves the ordinary structured-parameter
requirement and common bounds. No inference or billing probe supplies quota.

Go queues one bounded five-minute observation per supported connected account,
using the last original native owner's machine. Individual and whole-inventory
refreshes are authenticated SubscriptionService operations with UUID-v7 receipts;
refresh-all derives its complete account set on the server, independent of client
pagination. An idle read takes a short exclusive credential lease. An active
execution uses its registered original Codex process and lease; it cannot create
a second credential writer. Explicit-original, omitted-machine, Refresh all and due
maintenance use the same active-execution eligibility. For that read, missing or
unverified saved installation discovery and numeric version metadata cannot deny
an already initialized direct-v4 execution. Admission and send/publication retain
the original account, connection, credential generation, Execute lease/revision,
server epoch, authenticated Worker device, current machine/instance, negotiated
managed subscription and observation capabilities, and initiating actor. The
Worker observation registry uses only the actual original initialized Codex
client; an absent registry owner permits no read or replacement process. Idle
quota, lifecycle operations and reset-credit consumption/reconciliation retain
their existing saved-installation and independent cleanup requirements. This
boundary adds no RPC, capability or database migration. Original native rate-limit updates pass the same
bounded projection. Failed observations preserve the last successful timestamp,
values and exhaustion state. Sparse null fields retain original values, window
identities and individual observation times. Comparable windows use their minimum
remaining fraction; elapsed resets cannot establish recovery.

The server-owned optional subscription observation records retain exact account,
connection, credential generation, machine, actor and original operation identity.
A durable send claim precedes every explicit native request. Claim replay never
grants another send. Queued operations become stale on credential generation
replacement, rather than silently inheriting a new generation. Stop, revocation,
pending lifecycle cleanup and lost native ownership retain their original fences.
Only confirmed native/file cleanup releases an idle credential lease.

Reset-credit inventory preserves the authoritative signed-64-bit count separately
from a bounded detail list. Null details mean unavailable; an empty list is an
observed empty list. Native credit IDs, reset type/status and grant/expiry times
are metadata; native titles, descriptions, unrelated billing content, account identities and token
reflection never enter resources, receipts, logs or history. The desktop requires
explicit confirmation tied to the displayed account revision, connection,
generation and inventory identity. The CLI requires `--confirm`, with either the
returned credit ID or explicit native next-credit selection when only the count
is available. Configuration saves cannot manufacture observations.

Codex quota reflection validation checks exact original bucket IDs after removing only the adapter-owned final `primary`, `secondary` or `spend` suffix, and exact reset-credit IDs, against every nonempty protected identity/token and its complete standard/URL Base64 forms at any length. Existing long substring checks remain; incidental short substrings stay valid. Worker explicit reads, rolling updates and server-owned quota reads share this guard before publication. Rejection preserves the last good quota and exhaustion state independently of native cleanup.

The accepted UUID-v7 operation is the official `idempotencyKey` for
`account/rateLimitResetCredit/consume`. Uncertain consumption never automatically
requeues or receives a replacement key. Explicit generation-checked
`ReconcileSubscriptionCredit` preserves that original key and selected credit.
Closed outcomes retain `reset`, `alreadyRedeemed`, `nothingToReset` and `noCredit`.
The latter two describe truthful non-reset outcomes. Every attempted consumption
performs a separate quota read afterward; failure retains previous quota and
cannot manufacture a recovery notification.

Recovery notification preferences default to false. Only a fresh observed
transition from confirmed exhaustion to positive usable quota creates an Inbox
entry, atomically with account publication. The original observation source index
deduplicates it across receipts. Recovery Inbox records have account/connection
scope and no session, interaction or execution authority. Existing independent
read-state and once-only notification presentation claims apply. Native delivery
accepts only the opaque original claim/inbox IDs and a closed recovery kind; its
fixed title/body reveal no account alias, provider identity, quota value or secret.
Presentation is available only while the trusted desktop process runs.

Validation must distinguish isolated native/provider processes, SQLite and
response-loss fixtures from installed real-account credit consumption, quota
recovery and platform notification acceptance. Keep the issues open until their
remaining real-environment acceptance is satisfied.

Quota maintenance performs a read-only due check and transactional eligibility recheck, publishing a durable receipt only for a changed batch. Account deletion transactionally removes its account-scoped recovery Inbox entries through ordinary event/tombstone publication. Idle quota/reset-credit publication uncertainty remains an original-operation problem when the unchanged valid bundle and independent native/file cleanup are confirmed; finish the credential lease without fabricating quota recovery, then permit only explicit same-key credit reconciliation. Failed credential capture or unconfirmed cleanup still fences the native owner.

Unpublished quota reads become failed after independently joined successful native credential/process/file cleanup. Preserve the last good quota values and allow a new explicit or scheduled read; consumption uncertainty remains reserved for reset-credit operations with the original official key. Migration 28 bounds relevant configuration/device metadata and checks affected original session/job references in SQLite without loading unrelated retained history.

### Native version and failure diagnostics

Codex sign-in attempts share the harness contract's minimum SemVer `0.151.0`, with no upper bound. Discovery of an executable, verified initialization and actual account login remain separate. Server operations retain optional safe `diagnostic` metadata in their existing versioned document; no SQLite migration is needed. Progress publishes it only to the authenticated original initiator. Retain the actual detected version, minimum, closed discovery/version/profile/runtime/launch/initialize/confirm/login/models/execution/history/cleanup phase, stable error code, locally reconstructed reason/guidance and original operation correlation ID. Validate closed metadata rather than retaining arbitrary Worker/native error strings.

Missing executables report discovery with no detected version. Invalid or lower versions report version validation; newer versions run actual initialization. Keep the first native failure when cancellation, process cleanup or protected-file cleanup subsequently fails; the independent recovery state and cleanup result remain authoritative. A cleanup-only failure uses cleanup phase. Persist metadata through restart and transient presentation disposal without login URLs, device codes, credentials, identity, paths or raw output. Success clears failure metadata. Logs retain bounded version/phase/code/operation correlation and separate original outcome from cleanup/recovery, without account identifiers. No diagnostic may trigger login replay, callback resend or automatic retry.

## Server-owned ChatGPT quota
System `SERVER_SUBSCRIPTION_QUOTA_V1 = 46` separately negotiates quota reads for idle server-owned ChatGPT accounts. For this issue, the owner's explicit batch instruction permits allocation and activation in one complete feature PR without a prior main reservation merge. This exception changes no other allocation, migration or native authority rule.

The existing QUOTA request can omit `machine_id` under capability 46. Explicit machines retain capability 18 and the strict Worker claim/receipt lane. An active execution uses its original Worker process and lease, including an omitted-machine request resolved to that original lease. Worker reset-credit consumption retains capability 19 and original Worker ownership. The separately negotiated server lane is defined for this feature below. Authentication success or capability 30 alone grants no server quota authority.

Optional `server_quota_generation` records eligibility only after successful original server login/authentication refresh and final protected-reference cleanup. First observation, complete Refresh all and five-minute maintenance derive eligible accounts on the server. A previous settled server generation can become eligible at startup only from its original successful non-native-active owner, valid current protected reference and confirmed reference cleanup. Missing, restored or uncertain evidence grants no eligibility. No SQLite migration or credential format conversion occurs.

The independent `server_quota` record retains original actor, server epoch, operation, finish receipt, connection, credential generation, request time and closed queued/sending/succeeded/failed/uncertain phases. A durable claim grants one bounded quota read in the verified original Codex process. The shared private-home adapter performs no inference, login, authentication refresh or credit consumption. It uses sanitized native environment and checks the explicit server network selection before native startup. The current subscription profile has no proxy adapter; non-Direct selections return Unsupported without direct fallback, network traffic or proxy credential access.

Server quota ownership excludes lifecycle requests, Worker credential grants, observations, configuration deletion, saved-storage diagnostics and database restore. Sixteen joined account tasks run independently; shutdown cancels and joins them before protected stores close. Original native/process/file closure and unchanged final authentication bytes precede the durable cleanup checkpoint and quota/result publication. Read failures after confirmed cleanup retain last-success windows/time and permit a later explicit or due read. Unconfirmed cleanup retains original recovery and cannot relaunch. Publication rechecks actor, generation and connection; stale or revoked reads settle as failed after cleanup without publishing windows. Restart never repeats a previous queued or sending operation. Independently checkpointed cleanup can settle a failed original read; absent confirmation preserves recovery.

Successful projection shares sparse-window merging, minimum remaining fractions and atomic recovery Inbox deduplication. Elapsed resets alone grant no recovery. Frontend and CLI refresh negotiate capability 46 before omitting the machine, retain exact revisions/selectors/retries and expose unsupported/failed/pending states. Capability 46 alone grants no server reset-credit consumption; that operation independently requires capability 49. Structured logs contain operation/generation references, closed phases, safe error codes and cleanup confirmation only.

The owner will perform real-account, installed-native, remote and platform acceptance separately. This batch's automated fixture/build validation does not establish those results; that skipped acceptance is nonblocking for this feature PR under the owner's explicit instruction.

Managed ChatGPT Sidechat follows [the feature’s protected Fork profile](cmds-delidev-sidechat-contract.md#managed-chatgpt-sidechat). EXECUTE Take admits only its exact claimed original Fork job. Protected Finish writes a metadata-only original-job receipt after actor/source/lease rechecks; that receipt gates child publication. Independent managed Fork follows the separate System 53 / Worker 29 profile in the Fork contract and uses the same original-job receipt lifecycle.

Managed subscription installation refusal describes missing verified Codex installation evidence, not an exact or minimum native version. Existing bounded version metadata, detected state, path, protocol, observation and capability predicates remain authoritative. Safe guidance directs the user to the selected Runner Device’s installed Codex and protocol verification without exposing native paths or output.

## Server-owned ChatGPT reset credits
Managed Sidechat admission rejects an active original server-credit owner before
creating a Fork job. Protected Take and Finish retain their original account,
credential generation and independent cleanup fences.

System `SERVER_SUBSCRIPTION_RESET_CREDITS_V1 = 49` independently enables explicit
reset-credit consumption for an idle eligible server-owned ChatGPT account.
Record allocation and complete implementation together under the allocation
workflow. Preserve capabilities 19, 30 and 46. Reuse RequestSubscriptionObservation
and ReconcileSubscriptionCredit without a new RPC, Worker capability, database
migration or credential conversion.

An omitted machine selects the original settled server credential generation
and protected AccountLogin reference. An active execution resolves an omitted
selector to its exact original Worker; explicit-machine requests retain the
Worker lane. Never manufacture a Runner, transfer accepted consumption or acquire
a second credential writer. Fresh inventory and explicit confirmation bind the
account revision, connection, generation and inventory ID. A positive count
permits an available unexpired exact credit, or explicit native next-credit
selection only when details are unavailable. Unknown, future or stale inventory
requires a successful quota refresh first.

The separate optional `server_credit` record retains the original UUID as the
official idempotencyKey, actor, epoch, connection, credential generation,
inventory identity and immutable credit/next selector. Each explicit attempt
has separate current server epoch, attempt/finish IDs, a durable send claim,
provider outcome and independent cleanup checkpoint. A monotonic ever-sent flag
retains possible earlier consumption even when reconciliation has not sent. Quota-only records retain
their meaning. The shared narrow native selector validates Worker and server
ownership separately. Verify actual native readiness, original authorization and
the configured Direct route before send. Unsupported routing fails before launch
without direct fallback, native traffic or proxy-secret access.

Receipt replay, status, polling, reconnect and restart never relaunch consumption.
Possible-send uncertainty retains the original key and selection. Only explicit
reconciliation by the still-valid original actor, connection and generation can
create a new attempt after independently confirmed original process/file cleanup.
The provider key and selector remain unchanged, including alreadyRedeemed results.
Unconfirmed cleanup keeps recovery fenced. Another actor or new inventory cannot
transfer a retained attempt.

Checkpoint reset/alreadyRedeemed/nothingToReset/noCredit before a separate quota
read and cleanup. Read failure cannot erase consumption, replace last-success
quota or invent recovery. Active credit ownership fences lifecycle, execution
credentials, account deletion, failed-subscription cleanup, saved-storage
inspection and backup/restore. The existing bounded controller joins native tasks
before protected stores close. Cleanup checkpoints remain independent of outcome
and finish publication; lost finish or restart cannot authorize another send.

Desktop Review, Confirm and original-operation reconciliation negotiate 49,
retain exact request retries and preserve English/Korean guidance and Settings
lifetime. CLI consumption/reconciliation require --confirm; omitted-machine
consumption independently negotiates 49. Missing support, zero/unknown inventory,
pending ownership and failures remain truthful product states. Logs retain only
closed operation/code/cleanup metadata without native content or credentials.
Fixture/build/package evidence remains separate from installed-native,
real-account, remote-machine and platform acceptance. Record revision, commands,
results and unresolved limits in PRs, issues and CI artifacts.

## Server-owned ChatGPT quota V2
System `SERVER_SUBSCRIPTION_QUOTA_V2 = 50` expands omitted-machine QUOTA, Refresh all and five-minute maintenance to the selected server, independent of Worker registration, connectivity, verified installation, retained Worker ownership or an active Execute lease. Record allocation, declarations, generated bindings and complete activation together in the owning feature PR under the allocation workflow. Preserve System 18, 46 and 49 and explicit-machine legacy QUOTA/reset-credit semantics. No Worker capability, RPC, credential conversion or SQLite migration is added. Clients require capability 50 before issuing the expanded omitted-machine or batch request; older servers receive compatibility guidance without an automatic Worker fallback.

V2 uses a separate quota-only native profile. Go reads the exact sealed AccountLogin generation, validates the original account/user commitment and supplies only access token, account and plan to official `account/login/start` external `chatgptAuthTokens` authentication. The native process uses ephemeral authentication, disabled plugins and the official provider, receives no ID or refresh token and writes no managed auth.json. Refuse every external-token refresh callback with one fixed safe error; rejected/expired access tokens fail the observation without renewing credentials. Native initialization includes bounded configuration and readiness reads and is not claimed to emit only the quota HTTP request. Validate and discard the pinned external login’s once-only `account/login/completed` success notification (`loginId`/`error`/`onboardingEntrypoint` null or omitted, typed `success: true`) before the quota request; failed, foreign, malformed and unknown completion fields fail closed. This private completion metadata grants no managed-login, callback or renewal authority. Use one bounded `account/rateLimits/read`, existing strict sparse projection and explicit server network routing. Unsupported server profiles fail with server guidance and never select a Worker.

The V2 durable quota operation records `access_only: true` to separate its ownership from historical managed-auth quota claims. Capture original actor, epoch, operation, connection and generation; recheck them before protected-reference use, before external authentication and before publication. Lifecycle changes, removal, actor revocation and unresolved account recovery retain their original fences. Serialize account admission and protected-reference capture only for bounded store/vault work; hold no account transaction or lock during native/network work. Cancel the original read on binding changes and discard stale results.

Access-only quota can coexist with an original Execute credential writer. Its durable process/cleanup obligation and retained AccountLogin reference are independent of that lease. Execution completion can rotate its generation, but must retain the captured observer reference until quota process cleanup is confirmed and its exact reference retirement is checked. Before terminal quota publication, account serialization validates the original operation/actor/connection/generation and deletes only an obsolete captured AccountLogin reference. Preserve the current generation, every other reference and any still-owning lease/observation/credit/native writer. Confirm the current reference exists and independently enumerate completed removal. Failed reference deletion or publication retains the original quota fence with its durable native-cleanup checkpoint; it never changes Execute ownership. Prior-epoch confirmed native cleanup retries only this bounded reference retirement before terminal settlement, without reopening native work. Rejected queued claims use positive no-native-work cleanup through the same retirement boundary. Quota cleanup uncertainty blocks another quota launch, lifecycle/reference deletion and database restore; it does not release, replace or fail the original Execute lease or account. Restart and receipt replay never send an accepted quota operation again. Startup reconciles prior-epoch quota cleanup only through the retained original process owner index and independently released controller proof, plus bounded original private runtime/probe cleanup. Missing ownership evidence remains uncertain; reconciliation never launches authentication or a quota read. Join quota tasks on shutdown. Historical quota claims without access_only retain their exclusive managed-auth interpretation and are never relaunched through V2.

Preserve last-success quota windows/timestamps on failures, truthful pending/failed/stale status, observed-recovery deduplication and bounded private reflection checks. Log only closed phases/errors/cleanup and operation identities, excluding authentication, user/account identities, quota values and native bodies. Synthetic protocol/ownership fixtures and build checks remain distinct from real native/account/remote/platform acceptance, which the owner performs separately and has explicitly made nonblocking for this batch.

## OpenCode Go subscriptions
The [OpenCode Go contract](cmds-delidev-opencode-go-subscription-contract.md) owns the exact key-backed `opencode_go` exception, fixed server relay profile, original native session header and independently confirmed cleanup. Identity 4, System 54 and Worker 28 retain separate ownership; System 52 remains Project behavior. Native login and quota authority remain unavailable. No migration is added.

Independent managed Fork now follows the separately negotiated System 53 / Worker 29 profile in [the Fork contract](cmds-delidev-forks-contract.md#managed-chatgpt-independent-fork). It reuses the exact accepted Fork EXECUTE lease and durable Finish receipt, including original generation, actor, cleanup and uncertainty checks. Legacy internal Sidechat-named receipt storage also serves this independent profile without changing existing receipt bytes or requiring a migration.

## Situation quota exhaustion notifications
Only ChatGPT quota observations can publish this metadata-only operational Inbox kind. The original connection must move from complete fresh known usable evidence to explicitly confirmed exhausted evidence in a strictly ordered successful observation. Unknown/sparse/stale/failed observations, credit reset, reconnect, import and restore cannot establish that transition. Notification preference and display claims do not alter per-account quota recovery consent or start recovery. Follow `cmds-delidev-inbox-contract.md`; native/account acceptance remains independently recorded.

## Automatic reset-credit consent
System `AUTOMATIC_RESET_CREDIT_CONSENT_V1 = 77` owns the closed
`SetAutomaticResetCreditConsent` revision-checked mutation. Worker
`CODEX_QUOTA_BLOCK_V1 = 52` owns bounded original failed-turn quota markers.
Record declarations and complete implementation together; no migration or new
native action is added. Existing manual consumption, server credit lanes and
explicit Resume retain their independent ownership.

Consent defaults off and binds the authenticated actor, account connection and
credential generation. Enabling requires explicit confirmation; disabling is a
separate exact mutation. Logout and every replacement credential generation clear
consent. Portable exports remove subscription state and restored credentials clear
it. Generic configuration saves must preserve the entire server-owned state, so
older clients cannot omit these fields or manufacture consent.

Only the closed original Codex `usageLimitExceeded` marker, or
`rateLimitExceeded` with fresh observed subscription exhaustion, can qualify.
Free text, context limits, session budgets, server overload and paid spend limits
grant no spending authority. The server validates the retained failed Execute
job, immutable selection, native thread/turn, Worker device/instance, server epoch
and original active credential lease before atomic admission. Sidechat, Fork,
idle processes, replaced Workers and already cleaned native sessions cannot spend.

One durable account episode owns at most one UUIDv7 reset-credit operation and
its original provider idempotency key. Repeated errors, status, restart and lost
acknowledgments do not replay sends. Bounded original-turn fences survive fresh
quota recovery so delayed redelivery cannot spend again. Fresh observed recovery
alone rearms a new exhaustion episode, independently of notification preference;
elapsed reset times do not rearm. At the 1,024 retained-marker bound, automatic
admission stops conservatively rather than evicting a spending fence.

A fresh positive inventory selects a copy of eligible, unexpired Codex credits by
expiry (no expiry last), grant time and full ID, preserving display order. A
positive count without individual details permits only native next-credit
selection. Stale inventory permits one read-only refresh on the same original
lease; missing, zero or failed results do not admit consumption. The retained
observer publishes and claims the admitted operation once, before joined native
cleanup. No idle Worker lease may adopt an automatic operation. Explicit
same-key reconciliation retains the original selector and provider key but removes
automatic lease authority and follows the existing confirmed-cleanup/recovery
contract. Consumption outcomes and quota recovery remain separate.

The approved Settings confirmation uses the existing task lifetime, retained
mutation, theme tokens and English/Korean copy. Closing Settings does not revoke
accepted server processing. No consumption resumes a failed session automatically.
## Paid-credit observations
System `SUBSCRIPTION_PAID_CREDITS_V1 = 76` and Worker `SUBSCRIPTION_PAID_CREDITS_V1 = 51` independently negotiate the narrow ChatGPT/Codex paid-credit projection. Reuse authenticated original quota reads, leases, connection/credential generations, actor checks, joined native cleanup and existing refresh actions. No RPC, purchase, consumption action or SQLite migration is added. An older Worker omits these fields; the server rejects a paid-credit publication from a Worker without capability 51.

Each native bucket owns its exact bounded ID, required hasCredits/unlimited flags, nullable balance and successful observation timestamp. Accept nonnegative plain decimal strings of at most 64 bytes without numeric conversion. Explicit null is unknown, zero is a real value, and unlimited takes presentation precedence. Never sum buckets or infer balance from quota, reset credits, plan type or hasCredits. Omitted credits/balance fields retain the last successful bucket and its timestamp; failed, malformed or reflected reads retain evidence without refreshing it. Credential-generation replacement clears observations. Ordinary configuration saves and legacy clients cannot replace protected subscription observations. Original identity/token reflection checks include paid bucket IDs and exact balance strings, including short/encoded secrets. Raw responses, unrelated billing/display text and credentials remain private.

Account rows show a compact Paid credits card above quota with one rounded balance or a localized multi-bucket count. Numeric primary values use locale grouping and exactly two fractional digits, with string arithmetic and half-up rounding. Positive source values below 0.01 display `<0.01`; actual zero displays `0.00`. The unchanged exact source string remains available through an information disclosure opened by hover, focus, click, Enter or Space. Escape closes the disclosure and preserves trigger focus. Unlimited takes precedence; failed reads without evidence show Balance unavailable and the failure explanation. Multiple buckets retain source order and separate original observation timestamps; never aggregate balances. The card uses semantic theme tokens, 8px corners, 12px secondary text and a 24px tabular balance. Manage subscription shows the read-only Paid credits card above Quota, with separate bucket labels and retained stale/error timestamps. Keep loading, unavailable/update, unknown, zero, positive and unlimited states distinct. Use existing theme tokens, wrapping rows, parent scrolling, keyboard/dismissal ownership and EN/KO strings. Presentation grants no account, execution, native or platform acceptance.
