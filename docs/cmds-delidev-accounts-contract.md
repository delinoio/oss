# DeliDev account lifecycle

## Connected API format changes

ProviderInventory capability 9 and `ChangeAccountApiFormat` were reserved on main
in the originating change. The owner/client operation receives an account ID, expected revision,
request ID, closed API protocol and editable preferences. Its transaction publishes
all preferences and the selected profile together with an actor-bound retry receipt.
An exact replay returns current account metadata without another generation or
protected-store operation. A conflicting revision or failed transaction publishes
nothing. Unknown formats, unsupported profiles, pending cleanup and keyless/key
ownership changes fail closed. Active inspections must settle before the change;
late original inspection results cannot validate a new connection ID.

A changed connected tuple creates a new connection ID with `credential_id` pointing
to the original protected reference. The previous connection/profile, health and
validation are retained in server-owned `retained_connections` in account JSON.
The new generation is unverified, with validation and catalog observations cleared.
Quota/exhaustion and ordinary account enablement remain shared controls. Saving
sends no external API request and performs no key Put/Delete/Enumerate. Explicit
Validate connection observes only the new generation and gates new-session
execution. At most 128 previous generations are retained; the next change rejects
without evicting original session authority. These runtime fields cannot be forged
or cleared through ordinary configuration writes or portable imports.

Existing executions, queued continuation, Resume, Fork, Sidechat, compaction,
auxiliary work and title generation resolve their original connection ID and
profile. New sessions resolve the current generation. Referenced current and old
profiles remain immutable. Incompatible configured Workers receive a server-owned
reconfiguration marker atomically; explicit compatible Worker saving clears it.
Ordered-route schema 3 may contain this marker, while remaining exclusive with
legacy routing fields. Existing executions require the original Worker to exist,
without reading its newly edited configuration or this new-session routing marker.

Explicit Disconnect or deletion revokes all generations, requests cancellation of
all unfinished account work, joins original credential users and uses the existing
confirmed cleanup path to delete the shared reference once. Failed deletion retains
the original retry obligation. OAuth completion receipts and refresh serialization
retain their original account/reference identity through any number of changes.
No SQLite migration, native change, Provider identity change or format conversion
is introduced.


## OAuth format selection extension

The [OAuth format reservations](cmds-delidev-account-oauth-contract.md#oauth-api-format-selection-reservations)
own the recorded capability 8 and Start/attempt format fields established
by reservation the originating change. Preserve manual
format profiles, original defaults and independent OAuth eligibility. The common
manual/OAuth connection UI requires selection for multiple profiles and displays
a sole profile read-only; server-owned Start pins explicit OAuth selections through
completion/recovery. Provider registration remains independently gated. No
database migration is added.

## Per-account API formats

API accounts may declare the closed `api_protocol` selection under the
[catalog contract](cmds-delidev-catalog-contract.md#api-account-format-selection).
Explicit selections use resource schema 3; legacy accounts retain their original
provider tuple. Connect stores the selected protocol/URL/authentication in the
immutable connection generation. Validation, discovery and execution resolve
that same tuple. Capability 9 changes a connected account for future sessions
without key input under the amendment below. Capability 7 alone requires
Disconnect, confirmed cleanup, explicit key input and validation. An account cannot change
between keyless and key-required authentication. Referenced profiles, including
legacy defaults and disconnected accounts, cannot be removed or replaced.

`account list --api-protocol openai-responses|openai-chat|anthropic-messages`
requires ProviderInventory capability 7 and composes with API/provider filters
before pagination. Unknown values and subscription selectors reject the request;
snapshot/event APIs retain their existing shape. Desktop preference patches may
add or replace the validated `api_protocol` token while preserving all protected
JSON tokens and uint64 values exactly. Clearing explicit format identity is
unsupported. Format edits preserve model/Usage/session identity and grant no
inference, native execution or OAuth authority.


A disconnected SQL record alone does not prove cleanup: a failed native Connect
may retain protected staging intents. Disconnected format-change admission holds the account
gate, checks the original revision and credential class, then verifies no remaining
native references outside SQLite before publication. Failed enumeration rejects
the edit. Exact accepted receipt replays do not reopen the vault. Keyless proof
skips native enumeration and cannot be relabeled as credential-owning authority.

OpenRouter OAuth uses the dedicated owner/client Start/Complete/Cancel/Status lifecycle in the [OAuth contract](cmds-delidev-account-oauth-contract.md). Durable completion receipts represent once-only dispatch, never retry authority. Exact original local recovery reads only its reserved protected reference. Account creation reuses configuration validation; final connection reuses the same locked transaction helper as manual Connect and atomically commits the private connected outcome. Defaults are OpenRouter/api/enabled, automatic selection allowed and recovery notifications enabled, preserving the approved API-creation default rather than changing existing accounts or subscription defaults. New connections remain unverified until explicit validation/discovery.

## Ownership and implemented scope

The server owns account state and credentials under [the feature](cmds-delidev-requirements.md). This contract currently implements API credential connection, explicit keyless local connection, disconnection, cleanup reconciliation and account status through authenticated Connect and the CLI. Bounded non-inference validation is defined in the [provider inspection contract](cmds-delidev-providers-contract.md). Automatic model catalog publication is defined in the [catalog contract](cmds-delidev-catalog-contract.md). Digest-only execution proxy credentials and first Codex Worker execution are integrated through the [proxy](cmds-delidev-proxy-contract.md) and [session](cmds-delidev-sessions-contract.md) contracts. Disconnect now durably cancels that account's unfinished native assignments; public first Codex API dispatch uses current validated connection readiness; Codex subscription login, refresh, execution and logout follow the separate [managed subscription contract](cmds-delidev-subscription-contract.md); complete native recovery, existing-login import, other subscription harnesses and quota refresh remain pending. Saving a credential is not provider validation or execution readiness.

Account aliases/provider associations and display/routing preferences remain configuration. Health, connection generation, validation/catalog observations, quota observations and pending removal are server-owned. General configuration writes must preserve those fields exactly; new accounts start disconnected. An account's provider/type cannot be relabeled through configuration, and a referenced provider's authentication/authority cannot be changed in place. Credentials never enter configuration documents.

Desktop edits of an existing Account patch only the top-level `alias`, `enabled`, `exclude_automatic`, `recovery_notifications` and validated optional `api_protocol` JSON tokens in the original resource bytes. General preferences, quota notifications and post-login naming share the bounded `account-preferences.ts` scanner. It validates JSON syntax and duplicate fields without converting protected numeric tokens to JavaScript Number values. All other tokens, including uint64 lease revisions above 2^53 and at the uint64 maximum, remain exact. Invalid or overflowing protected numbers are never rounded or repaired; Go retains numeric schema validation and exact protected-observation comparison. Uncertain replay retains the original UUID, protobuf revision and complete resource bytes. Stale revisions and unauthorized or protected-field changes remain rejected without changing account state.

## CLI and RPC

`account list` uses the ordinary paginated `ResourceService.ListResources` path and may select `--account-type api|subscription` and `--provider-id UUID`. Both selectors are optional for compatibility; subscription lists reject any Provider selector. The server composes supported selectors before pagination and binds them to the cursor; clients must not filter a fetched page locally. Snapshots and event streams remain unfiltered and unchanged.

| CLI | Connect RPC | Meaning |
| --- | --- | --- |
| `account connect --id ID --revision N --key-stdin` | `AccountService.ConnectAccount` | Store a bounded API key through the protected server vault and record an unverified connection. |
| `account connect --id ID --revision N --keyless` | `AccountService.ConnectAccount` | Explicitly connect a provider already configured with keyless local authentication; no native secret is written. |
| `account disconnect --id ID --revision N` | `AccountService.DisconnectAccount` | Commit disconnection, remove every remaining protected reference owned by the account, then clear the cleanup marker. |
| `account status --id ID` | `AccountService.GetAccountStatus` | Read current metadata without consulting or unlocking the native credential store. |
| `account validate --id ID --revision N` | `AccountService.ValidateAccount` | Record a bounded non-inference endpoint/credential observation for the current connection. |

Mutations accept `--request-id UUID-V7` and return the existing version-1 CLI envelope. They never start a server implicitly. Worker credentials cannot invoke any account RPC. Owner and paired client authorization is rechecked before secret staging, at state/receipt commit and before cleanup completion; revoking an in-flight client cannot publish a connection after a native write.

API keys contain 1–8,192 printable non-space ASCII bytes. The CLI accepts one optional LF or CRLF terminator from stdin and rejects extra lines, whitespace, control characters, oversized input and empty input. A terminal without supplied stdin returns missing input instead of requesting interactive authorization. There is no API-key argument or generic configuration-file key field. `--key-stdin` cannot share stdin with `--token-stdin`; use the private owner scope or a paired client. Both sides clear their decoded secret buffers after bounded use. These measures do not promise complete heap erasure or an OS sandbox.

Provider authentication determines whether a key or explicit keyless selection is valid. Keyless providers remain loopback-only, and localhost means the server machine. A subscription account receives an explicit unsupported response from this API operation; it cannot use an unrelated system login or reinterpret an API key as subscription authentication.

## Connection and readiness

A connection requires the current account revision, disconnected health and no pending cleanup. A new immutable connection generation equals the connect request UUID-v7. Its metadata contains the provider's authentication mode and connection timestamp; the raw key remains in the [protected vault](cmds-delidev-credentials-contract.md). Connection sets `health=unverified`, clears prior validation/catalog and quota/exhaustion observations and does not grant routing eligibility. The routing boundary accepts only ready accounts with a current connection and no pending removal, so a stored credential cannot authorize execution.

An explicit disconnect with completed cleanup is required before replacing a connection. Connection metadata cannot be changed through an ordinary configuration edit. Native/file work runs outside SQLite transactions. The server serializes account connection, disconnection and configuration deletion/edit boundaries; competing connection requests with the same expected revision cannot stage multiple active connections.

Before staging credentials, the server checks a read-only durable receipt lookup and validates authorization, account revision, type, provider and state. The final SQLite mutation repeats the checks and commits connection metadata, events and the receipt together. If a native write succeeds but SQLite acceptance fails or is uncertain, the reference remains staged. It is never speculatively deleted: retry the same request or explicitly disconnect to reconcile all of that account's protected intents. An account with outstanding intents cannot be deleted through general configuration.

The connect receipt input binds the exact secret with HMAC-SHA256 using the server's existing private durable owner identity, with a distinct account-connect domain, server scope and request identity. SQLite retains neither raw key bytes nor an unkeyed secret digest. This request commitment remains comparable after the per-credential wrapping key has been deleted. Restoring a scope must preserve its matching private owner identity; changing that identity is not a credential rotation API.

## Disconnection and retry

Disconnection has two durable state changes:

1. The acceptance transaction clears the connection, sets disconnected health, clears validation/catalog and quota/exhaustion observations, cancels unfinished assignments selected on that account and writes a removal marker plus its receipt. The public marker contains the original request ID and expected revision, so a client can reconstruct an exact retry from account status. A separate private cleanup receipt identity is retained only inside the server receipt, never in public account metadata.
2. Outside the transaction, the server enumerates only the account's own unremoved vault references, including interrupted staging, and deletes them through the vault's durable tombstones. A second transaction rechecks authorization and the pending disconnect identity, preserves any newer metadata edits, removes the marker and emits the final revision/event.

Execution cancellation is paged over exact selected-account assignments, including pre-grant queued work and uncertain ownership. Candidate routing links, other accounts and terminal history are excluded. Undispatched jobs become canceled; claimed envelopes retain their exact revision/digest and receive a separate durable cancellation control, including on reconnect. Sessions pause and retain execution/input ownership. Existing uncertainty stays recoverable, and cancellation alone neither completes Archive nor proves native acceptance or process cleanup. Old disconnect receipts never recancel work created on a replacement connection.

Before key deletion the server cancels and joins active relay handlers. The Worker independently receives its cancellation, joins owned native cleanup and journals/reports its outcome. Missing native terminal/cleanup publication retains session recovery even when relay/key cleanup succeeds; these are separate confirmations.

No keychain is silently unlocked and no unrelated native credential is enumerated. If the native store, filesystem or transaction is unavailable, the account remains disconnected with a retryable removal marker. `DisconnectAccount` returns an accepted disconnection response containing current account metadata and a typed sanitized cleanup problem. The CLI keeps that result and request ID while returning the problem's nonzero exit code. Retry the original `account disconnect` request with the original expected revision and request ID; a new request while removal is pending conflicts and points to the recorded operation.

The public API removal marker's `expected_revision` remains an unquoted uint64 JSON integer. Desktop recovery reads its original bounded numeric token directly into bigint and preserves the original UUID-v7 request ID. A fresh mounted status view can retry revisions above JavaScript's safe-integer range without changing the request. Malformed, overflowing or conflicting markers grant no retry authority; the server alone clears the marker after confirmed secure deletion.

Successful cleanup permits reconnection or configuration deletion. A validated keyless API provider skips vault opening/enumeration/deletion: account provider/type and referenced-provider authentication are immutable, proving that no credential could have been staged during that account lifetime. This proof remains available after connection clearing and restart; relay cancellation and durable cleanup receipt checks still apply. Credential-bearing accounts, including disconnected accounts with potentially staged intents, continue to require vault reconciliation. A completed deletion tombstone no longer blocks account deletion, but remains durable in the private vault. Deletion still validates every configuration/session/schedule reference: live Agent account lists, project account restrictions, retained session first/current selections, immutable snapshot candidates and routing observations, and tombstone-bound project restrictions. Archived sessions remain references. Schedules reference accounts through their Agent/project selections and derived retained sessions; they have no separate account selector. The deletion transaction repeats the complete relationship check so a concurrent new reference cannot be left dangling. A metadata edit made while cleanup is waiting must survive the original disconnect's later completion.

The desktop API-entry deletion controller in `api-account-deletion.tsx` composes `GetAccountStatus`, `DisconnectAccount` and `DeleteConfiguration` after one explicit current-entry confirmation. The first fresh read must match the confirmed revision, immutable Provider identity and user preferences. Even a disconnected credential-bearing account goes through disconnection to reconcile staged intents; keyless accounts retain the existing server-owned vault bypass. An existing removal marker reconstructs only its original UUID-v7 and exact uint64 expected revision through `accountRemovalMutation`. Malformed or conflicting markers grant no new cleanup request.

A matching original disconnect acknowledgment and independently confirmed credential cleanup permit a fresh status read. Only the same API account/Provider/preferences, a nonregressing bigint revision, disconnected health and absent connection/removal permit one latest-revision configuration deletion. Cleanup problems retain the account and expose an explicit original-cleanup retry. Transport or malformed-acknowledgment uncertainty preserves the original request bytes, including after a later definite replay rejection; no new identity may replace it. Read failures after confirmed cleanup offer only a read retry. Reconnection, preference changes or a definite first-attempt conflict require fresh inspection and explicit confirmation. No renderer account-field write, provider validation, inference, protocol allocation or migration is introduced.

API deletion retains the existing category-owned task lifecycle: X/Escape hide a pending or unconfirmed original operation, while category/Settings departure disposes follow-up deletion authority without undoing accepted cleanup. Confirmed deletion releases retention and invokes completion once before refreshing the current list; browser/device cleanup remains independently owned and never delays dialog closure. Existing complete configuration/history/reference checks remain authoritative. The API-specific failure presentation exposes the sanitized server reason and guidance rather than hiding every conflict behind a revision-change summary. Structured `configuration_delete_rejected` logs contain only a closed phase, stable error code and validated original request/correlation IDs, excluding account documents, aliases, connection identities and native/provider error prose.

The desktop ChatGPT account deletion controller composes existing server-lane `RequestSubscription(LOGOUT)`, original `GetSubscriptionProgress`, fresh account reads and `DeleteConfiguration` after one explicit confirmation. It waits for the original successful logout and cleared native/credential ownership, preserves the confirmed configuration preferences, and passes the latest bigint account revision to deletion. Existing server vault, authorization and complete reference checks remain unchanged. Uncertain mutations retry only their original IDs/bytes; departure stops client follow-up deletion while accepted logout continues. The subscription Settings contract owns the presentation and category lifetime. Other subscription deletion workflows retain their existing dedicated native cleanup requirements.

Failed initial ChatGPT server logins also become disconnected after the subscription contract's independently confirmed native and protected-credential cleanup. Existing recovery metadata is reconciled only for the original login without a connection, generation or Worker owner. Native cleanup checkpoints survive vault failure and restart; uncertain or foreign ownership remains blocked. The deletion screen can explicitly request server-owned cleanup and deletion of that original failed initial LOGIN. Its durable child preserves the confirmed public revision and original requester/login; only cleanup checkpoints advance the effective revision used for account deletion. A recorded terminal failure requires fresh observation and confirmation; it is not an uncertain transport retry. Maintenance never deletes account configuration or relaxes its revision, reference or browser-cleanup checks.

Receipt replay never repeats native staging or deletes a replacement generation. Replaying an old connect after disconnect returns current disconnected metadata without recreating credentials. Replaying an old disconnect after a later connection leaves the new connection untouched. A deleted account cannot be recreated by any of its previous requests. Reads and replays retain current revocation checks. Server shutdown waits for the account operation boundary and closes its owned vault before releasing the data scope or recording the final stopped event.

## Verification

Real loopback Connect and temporary SQLite tests cover exact retries across restart, different-secret conflicts, concurrent connections, keyless/type/authentication guards, forbidden configuration health changes, Worker denial, client revocation during native staging, orphan-intent deletion guards, failed cleanup across restart, later metadata edits and old receipts after reconnection/deletion. The secret backend used for fault injection exists only in tests. Production always uses the OS-backed vault.

The CLI's keyless workflow is tested through the real server, and secret-input tests cover bounds, line endings and conflicting stdin consumers. A separately opted-in native CLI test runs the compiled CLI/server with real Linux Secret Service inside the disposable container described in the credential storage contract. It verifies protected stdin input, restart/replay, disconnection, deletion and secret-free retained server files/output. It makes no provider/model/inference requests.

To run that native CLI check after building the credential test image, build the CLI and test binary for the Docker daemon's native architecture into a fresh temporary directory, and mount only those test artifacts read-only:

```sh
delidev_cli_fixture_dir=$(mktemp -d)
chmod 755 "$delidev_cli_fixture_dir"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$delidev_cli_fixture_dir/delidev" ./cmds/delidev-cli
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c ./cmds/delidev-cli/internal/cli -o "$delidev_cli_fixture_dir/cli.test"
cp cmds/delidev-cli/internal/cli/testdata/secret-service/run.sh "$delidev_cli_fixture_dir/"
docker run --rm --network none --mount "type=bind,source=$delidev_cli_fixture_dir,target=/opt/delidev-tests,readonly" --entrypoint dbus-run-session delidev-secret-service-test:local -- /opt/delidev-tests/run.sh
rm -rf "$delidev_cli_fixture_dir"
```

Choose `amd64` instead when appropriate. This opt-in test must never target a host user's shared credential session. Native Windows/macOS account-command composition, subscription/provider authentication and execution/proxy lifecycle evidence remain separate from this Linux credential-lifecycle result. Check the actual validation results and unresolved limits in pull requests, issues and CI runs before making a support/completion claim.

API provider activation is independent of account enablement, connection, validation, and credential cleanup. Provider inventory derives exact total/connected counts server-side; connected excludes accounts with pending credential removal and does not imply verified health. `ListResourcesRequest.provider_id` and the closed `account_type` selector are list-only account filters applied before pagination; signed cursors bind both selectors, while snapshots and event streams retain their prior scope. The desktop uses separate AI Subscription and AI API Keys categories. The API category requires the provider inventory's account-type-filter marker; AI Subscription independently requires System capability 17 and never queries Provider inventory, search or discovery. API provider Add AI API key opens the Details form directly with the exact enabled saved inventory entry; the creation picker uses immediate native-button navigation with independent unfiltered bounded provider pages as specified in the desktop contract; it saves metadata once, then performs a fresh provider read immediately before one explicit credential connection. Operation-local controls remain locked while that check is in flight; category navigation can dispose the workflow without implicit connection or business cancellation. It never starts validation or model discovery. AI Subscription creates schema-2 service-native accounts with disconnected state and recovery notifications disabled. Its managed ChatGPT controls explicitly request Codex login, cancellation, authentication refresh and logout through the original native owner. Authentication refresh is distinct from quota observation. Category departure or leaving Settings revokes scoped callbacks without implicitly canceling an accepted native operation; CLI and protected Worker execution follow the same managed subscription contract.

## Desktop terminology

AI API Keys includes keyed API entries and keyless local connections over the existing Account resource. API-only frontend copy uses entry nouns; connection, validation, health, discovery and cleanup remain independent. Keyless retry guidance refers to the local endpoint, never key reentry. Shared presentation retains subscription and mixed routing/selection terminology, original aliases and server diagnostics. Account RPC/CLI names, document fields, IDs, revisions, filters/cursors, exact retries and protected credential ownership stay unchanged. The [desktop contract](apps-delidev-desktop-contract.md) owns exact labels; record validation in the feature, its pull requests and CI runs.

## Independent subscription identities

The feature uses the closed service-native account/model contract in [managed subscriptions](cmds-delidev-subscription-contract.md). `SaveConfiguration` requires schema 2 for subscription accounts/native models and schema 1 for API configuration, rejects mixed identity families and preserves exact request/revision receipts. System capability `SUBSCRIPTION_SERVICE_ACCOUNTS_V1` (17) negotiates this independent support. Metadata-only saves remain disconnected. CLI JSON configuration infers the matching document schema; generated Go/TypeScript descriptors expose the same service enum for new usage, pricing and diagnostic attribution. The CLI result envelope itself remains version 1.

## Failed initial subscription deletion

The [subscription batch contract](cmds-delidev-subscription-contract.md#failed-subscription-cleanup-reservations) admits failed initial ChatGPT server logins and fully disconnected subscription configurations for all supported services. Disconnected configuration children carry no synthetic LOGIN identity; pending/recovery/native/Worker ownership and protected vault references still prevent deletion. Batch and public configuration deletion share `checkAccountDeletionLocked` and `deleteConfigurationTx`, retaining original vault intent checks, exact revision, complete configuration/history references, schedule updates, tombstones and browser obligations. Confirmed batch deletion records its per-account outcome and parent counts in the same deletion receipt transaction. Original actor revocation or account/login changes preserve the account; cleanup never substitutes server bookkeeping authority for the requester's deletion authority.


## Automatic API connection verification

Enabled connected API accounts acquire non-inference current-connection validation through the joined server maintenance owner under the [provider verification contract](cmds-delidev-providers-contract.md#automatic-api-verification). Missing evidence is immediately due; subsequent checks use persisted completion plus max(15 minutes, Retry-After). Provider disablement, account disablement, removal and changed connection/profile/revision fence automatic publication. Existing explicit validation, actor-bound replay, quota/exhaustion and immutable execution generations retain their authority. A readable saved key or successful public model list alone cannot become verified authentication.

## OpenCode Go subscriptions
The [OpenCode Go contract](cmds-delidev-opencode-go-subscription-contract.md) owns the exact key-backed `opencode_go` exception, fixed server relay profile, original native session header and independently confirmed cleanup. Identity 4, System 54 and Worker 28 retain separate ownership; System 52 remains Project behavior. Native login and quota authority remain unavailable. No migration is added.

## cmds/delidev-cli constraints

- Key-preserving API format changes follow `cmds-delidev-accounts-contract.md#connected-api-format-changes` after main reservation the originating change. Capability 9 owns the atomic format/preferences RPC; preserve capability 7 and OAuth reservation 8, bounded server-owned generations sharing the original protected key, immutable original execution/continuation profiles, explicit current validation, all-generation cleanup and portable exclusions. Observation-only edits rebase protected fields/revision while retaining drafts; editable conflicts block saves. No migration, native change or format conversion.

## cmds/delidev-cli/internal/cli constraints

- `session switch-account --id ID --revision N --account-id ID` invokes only the explicit owner/client Connect operation. Gate support with the typed status capability, preserve exact request identities, and leave the selected session paused until explicit Resume.

## cmds/delidev-cli/internal/domain constraints

- Capability-9 API format generations follow `cmds-delidev-accounts-contract.md#connected-api-format-changes` after main reservation the originating change. Resolve immutable execution connection IDs against server-owned retained profiles, share the original protected key reference, and keep current validation separate. Preserve actor-bound atomic retries, all-generation confirmed cleanup, old-client protection and portable/restore exclusions. Ordered-route reconfiguration markers block new routing only. No SQLite migration or native change.

- Codex diagnostics are optional bounded existing-document metadata with closed phases/codes and locally reconstructed safe text. Validate actual version attribution and original operation correlation; no diagnostic grants native/account authority or requires a SQLite migration.

## cmds/delidev-cli/internal/harness/claude constraints

- The feature permits only transient nonblocking descriptive observations from the original interactive native process and validated initialization/settings boundaries. Preserve exact native/protocol/account/input/history/cleanup checks; observed stage success grants no input or recovery authority, and no helper/probe may stand in for agent launch. Follow the startup and harness contracts.

## cmds/delidev-cli/internal/harness/codex constraints

- Managed account/read accepts the known optional nullable workspaceRouting metadata from Codex 0.159.2 through one strict shared response profile. Validate its bounded HTTPS origin, closed routing enum and exact bundle account identity; logout requires explicit account null, no routing object and independently absent auth.json. Discard routing metadata without changing Go endpoints or authentication authority. Keep unknown/duplicate fields rejected and preserve existing recovery fences. Managed failure logs distinguish closed login-start/login-completion/login-cancel/account-read/bundle-validation/local-logout stages without native content, identities, URLs or paths.

## cmds/delidev-cli/internal/server constraints

- Configuration deletion rejections log only closed phases, safe error codes and validated request/correlation IDs through `log/slog`. Preserve existing account admission, protected-intent, current-revision and complete reference checks; never log raw resource/alias/connection content or native/provider error prose. Desktop API deletion composes the existing disconnect and delete RPCs and does not grant new server cleanup authority. Follow `cmds-delidev-accounts-contract.md`.

- Continuation construction clears the predecessor's one-shot remediation authority before validating current execution selection, including explicit account switching. Only a fresh exact input/attempt binding can attach PR Git authority to the successor; preserve the complete original checkpoint account/connection independently.

- Lifecycle Take retains its original positive account observation across exclusive-lease cleanup and later metadata edits. Reject future revisions and revalidate the exact uncanceled queued operation, action, machine and initiating actor before granting; canceled, replaced or recovery-required state never acquires authority from a stale observation. Keep current-revision owner requests and exact claimed-job execution revisions unchanged.

- Publish GrokClosedInput accounting only in the original independently verified completion transaction, after matching original input, response, closed history and owned cleanup. Require successful product outcome and no original job cancellation at that commit, including Stop/Archive accepted after native success but before cleanup retention. Preserve native success/cleanup separately and retain units committed before later controls. Echo explicit NATIVE_UNITS_V1 reads, retain legacy response-only coverage and bound the combined units/groups/wire sizes. Advertise native accounting independently with capability value 4; never price Grok categories.

- Context reads may project only the original validated Codex root last-request total as an optional exact-decimal native snapshot; keep current_tokens null. Bind original assignment/account/Worker/native identities and sequence, use closed latest/historical status, mark later input/compaction/execution and failed/in-flight refresh observations historical, and preserve polling/Refresh as read-only. Never infer exact occupancy, model capacity, billing, native support or compaction authority. Follow the context, usage and desktop contracts.

## cmds/delidev-cli/internal/store constraints

- Public Steer must use the dedicated exact queue-revision/execution/turn boundary with the original mode and current account/Worker/native authority. Atomically claim only the selected input and preserve other FIFO positions. Keep one unresolved attempt, bounded metadata-only controls and reference-only actor-bound receipts. Journal claim/send/observation before each side effect; automatic inspection after uncertainty never permits a blind resend or start/cancel/resume fallback. Publish exact claim-bound delivery once, retain any later native resolution separately, and preserve independent recovery. Unclaimed cancellation returns input to FIFO; claimed cancellation/terminal/loss cannot fabricate rejection. Include every confirmed same-turn input in ordered progress/checkpoints and later history verification. Steer records/journals belong to future session deletion and backup.

- Public approval responses require the dedicated original-revision owner/client RPC, exact offered decision or restricted explicit-scope grant, native encoded-size preflight and current execution/account/Worker authority. Keep actor-bound reference-only receipts, separate question/approval response fields and metadata-only active-job controls. Worker claim/send/observation journals precede side effects; lost acknowledgments or existing journals never authorize replay. Closure/loss cancels queued responses or retains claimed uncertainty. Publish one claim-bound transport observation and preserve terminal acceptance recovery independently of native success and owned cleanup. Approval journals must join future coordinated session deletion/backup.

- Schema 25 implements the reserved Grok accounting migration with an independent layout marker; reject unmarked unmerged v25 files without modification. Insert one GrokClosedInput only with the original verified completion receipt and independently confirmed cleanup. Preserve history, exact counters, original assignment attribution, atomic replay and future-only retention; no backfill, pricing or budget writes. Follow the usage/storage contracts.

- Migration 27 adds the closed metadata-only request diagnostic table after real accounting 26. Reject foreign preexisting layouts, preserve original publication/revision/index parity, atomically publish session invalidation and reference receipts, and never backfill historical requests or evict records to admit new work.

- Migration 28 bounds relevant configuration/device metadata, not total retained history. Check original affected account references and unsettled session/job ownership directly in SQLite without loading or charging unrelated messages/jobs against retirement bounds.

- Claude native profile/owner/generation/operation and keyed identity metadata are optional Account JSON with no migration. Never persist login URLs/codes, raw native identity or authentication files. Restore quarantines profile references and removes readiness; portable configuration omits ownership. Deletion requires independently confirmed original native cleanup and no profile/owner, generation, lease, pending or recovery under the subscription contract.

## cmds/delidev-cli/internal/worker constraints

- Grok's private Worker mutation journal binds the immutable publisher's assignment, selected account/connection and original creation/input request IDs. Persist creation, native session binding, original input and native prompt binding in that exact order; compare input digests with the immutable assignment and never retain prompt/credential/path contents in this metadata. Synchronize before native work, fail closed on drift or uncertain persistence, and refuse a second writer or reopening even an empty retained journal. Read-only reconciliation cannot recreate send, publication, completion or continuation authority.

- Follow `cmds-delidev-accounts-contract.md` for API account lifecycle. Only dedicated authenticated account RPCs may change connection/removal/health state. Persist disconnection before native cleanup, preserve exact request retry information, keep internal cleanup receipt identities out of public metadata, and never replay a native write or remove a newer connection on an old receipt. Account deletion must recheck live Agent/project and retained session snapshot/routing/selection references, including archived sessions and deleted-project policies, at commit. Stored credentials remain unverified until actual provider validation; future proxy/session work must recheck current connection generation and readiness.

- First-execution snapshot/claim is an internal transaction primitive used by the public first dispatcher. Never mark a session ready from fixture evidence or installation detection. The coordinator must validate current native/account/workspace/Worker authority and compare the exact selected configuration at commit. Resolve templates and routing at first claim, preserve exact contents/order and requested options, and atomically retain immutable configuration, initial account/connection, actual route, input/native request identities and routing state. Later edits cannot rewrite this snapshot; current restrictions still apply. Claims do not prove native acceptance or free queue capacity. Keep receipts reference-only, preview pure, and any failed claim free of partial snapshot or routing changes.

- Account disconnect must atomically cancel unfinished native assignments selected on that account, not candidate routing links or completed history. Keep claimed job revisions/digests unchanged and use durable targeted controls across reconnect. Pause affected sessions and retain unresolved input/cleanup ownership. Join relay cleanup before credential deletion, but never present that as Worker process cleanup. Receipt replay must not cancel new work or remove a replacement connection.

- Accept non-secret question responses only against the exact open original interaction revision and current execution/account/Worker authority. Preserve exact answer arrays, explicit empty answers and immutable response identities; reject secret requests before ordinary persistence. Server acceptance is queued only, never native delivery. Native closure cancels a response still in the durable queue without erasing its content or requeuing on receipt replay. The dedicated owner/client RPC must revalidate its principal at commit/read-back, preflight the native encoded size and bind its reference-only receipt to the actor, original revision and canonical response. Exact accepted retries return current interaction state without renewing native authority or requeuing closed responses. Response controls cannot queue behind the execution waiting for them.

- Public continuation must preserve the first snapshot and route, and retain the original account/connection unless changed by the explicit stopped Codex API operation, while advancing a distinct current execution. Claim only the oldest queued input after exact accepted predecessor progress/version-2 completion/checkpoint proof and current Worker/account/model/project/readiness checks. Retain predecessor progress in the immutable successor job before replacing live progress; never reread changed Agent/templates into the snapshot or rerun routing. Failure rolls back the entire claim, and blocked diagnostics cannot erase concurrent controls. Healthy success may retain automatic FIFO intent; failure/interruption/Stop/Archive/Restore/recovery stay paused. Explicit Resume may retain intent for future input only after current readiness checks, and old receipts cannot reapply it after a later Stop.

- Claude manual-command process replacement must pin each originally verified closed transcript as an exact immutable prefix. Retain synthetic Resume context separately from input, output and accounting; require its exact native shape, original parent and next-input ancestry. Only that proved successor and an original failed action prefix may retain its unchanged local-command diagnostic outside the newly selected context. Never derive failure or Resume authority from human text, rebuild missing prior input, rewrite native bytes or serialize this live capability as crash-recovery authority.

- OpenCode Worker mutation journals are metadata-only, owner-private and bound to the original opened execution publisher's immutable server/device/instance/job/revision/configuration/account/connection references. Persist each exact original request/kind/body digest and native owner before one mutation; never reopen a retained initializer journal as fresh execution authority, including an empty one. Keep create/input order, unique response/arrival identities, Stop answer exclusion and original-Stop recovery bounds. A changed file or uncertain write latches the live writer; read-only reconciliation requires exact canonical retained bytes and original reference equality and grants no execution, acceptance, account or cleanup proof. Public OpenCode dispatch remains a separate integration.

- OpenCode creation response loss may reconcile only the original live attempted mutation after successful claim synchronization. Require one exact marker/settings candidate, independently empty history/status, unused initial accounting and matching direct identity before binding; remove temporary read routes afterward. Missing/transient evidence stays unconfirmed, contradictory evidence stays latched, and stored creation cannot fabricate an HTTP acknowledgment, repeat creation/input, adopt restarted state or grant general discovery authority.

- Registered OpenCode relay authority is limited to the pinned first-input Chat Completions profile and exact supported Build/Plan settings. Reuse every current job/input/session/account/Worker/epoch check, refuse protocol translation or unsupported explicit options, and cancel/join provider requests before protected-key deletion. Actual native registration evidence cannot enable public dispatch, reconstruct native acceptance or substitute relay cleanup for original process cleanup.

- Public OpenCode checkpoint continuation keeps the immutable first snapshot/route/account and claims only the oldest queued input after accepted version-2 predecessor completion. Recheck current authority and the exact queued supported workspace input mode before committing a successor; compare the preceding native agent independently from its retained input mode. Each new job owns fresh process/runtime/relay/outbox/claim identities; verify its predecessor's exact checkpoint, original finished/reported operation journal, fully acknowledged outbox and native mutation claims while holding the continuation workspace lease. Preserve the original native creation marker across new resume claims. Version-2 private envelopes additionally bind the exact assignment-input digest and initial history execution; never upgrade older files/reports. First and resumed journals remain distinct, a resumed journal permits one exact predeclared resume claim before one fresh input, and unconfirmed session publication blocks input. Failed/stopped sessions require explicit Resume; unchanged ready success may continue FIFO. Unsupported tool/project/auxiliary history retains version-1 pause until its full native profile exists.

- Claude first-input publication requires a fresh exclusive metadata-only claim journal bound to the exact immutable Worker assignment/account/connection. Synchronize one original input intent before native transmission; a retained journal or an uncertain write cannot authorize another send. Publish session identity/settings only from validated native initialization plus applied-settings evidence, then accept only the exact original user replay and native v4/v7 turn. Keep missing effort distinct from explicit values. Outbox acknowledgment retries preserve the same receipt without native I/O, and late acceptance cannot clear Stop/recovery or restore revoked relay authority. Content, usage, interactions and terminal publication remain separate adapters.

- Manual PR Git execution follows the integration/workspace contracts. Only an immutable supported assignment can prepare the closed native Git bridge under the original execution lease. Preserve Worker Git auth separately from harness account context, exact fork/ref/base/head and rebase lease, one synchronized push claim, bounded joined child ownership and independent post-native push proof. Missing/uncertain proof cannot grant handled state or replay.

- An acknowledged protected Finish may retain a fenced account. Keep execution uncertain unless final bundle capture and independent cleanup both succeeded, or the separately verified pre-native cleanup returned the unused original bundle. RPC acknowledgment alone cannot authorize ordinary job reporting or overwrite the started claim journal.

- Managed Codex built-in connector startup metadata is consumed only after the harness validates the exact original root and closed private profile. Discard it without public events, account-health changes, tool authority or input/completion claims. Other MCP families remain unsupported until their independent typed adapters exist.
