# DeliDev automatic session titles

## Scope

`cmds/delidev-cli/internal/domain`, `internal/server`, `internal/store`, `internal/apiproxy`, `internal/worker`, and `internal/harness/codex` own automatic title state, attribution, execution, and cleanup. The canonical product requirements are issues #1056 and #1057; conversation execution and UI contracts remain in their existing documents.

## Runtime and Language

The Go server owns title policy, queued work, authorization, persistence, and usage attribution. The paired Go Worker runs the selected native Codex harness. The React desktop client requests automatic naming through the generated Connect client and renders retained status.

## Users and Operators

Owner/client sessions may opt into automatic naming. An authorized outbound Worker executes the title only when it proves the pinned profile and is the original Worker process. The server remains authoritative for capability, current policy, cancellation, budget, and ownership.

## Interfaces and Contracts

`CreateSession.name_mode` is the closed `manual`/`automatic` enum. Omission remains the existing manual request shape and receipt fingerprint. Automatic mode omits `name`; acceptance returns the server-owned `New session` placeholder, automatic owner, generation 1 and `waiting` status immediately with the original first input. Caller-supplied automatic names fail validation. Existing CLI/scheduled creation and manual names remain unchanged.

The additive `SystemService.GetStatus` capability `AUTOMATIC_TITLES_V1` describes server schema support. It is independent of the per-Worker `AUTOMATIC_TITLES_CODEX_V1` capability, which is offered only after an explicit Worker verifies the exact resolved executable from its server-returned detected Codex installation at version `0.151.0` and its native thread protocol. An unrelated executable on the sanitized `PATH` cannot stand in for a configured Codex path. The Worker first attaches without the optional capability, probes the returned installation, then reattaches with the capability only after verification; it opens the auxiliary stream only when that second response echoes the capability. A probe result of `recovery_required` stops the connected Worker before it enters the ordinary execution lane; a definite unsupported profile may continue without title capability. Other harness, version and authentication profiles stay unsupported. Capability negotiation is separate from the ordinary execution stream.

The first successful response turn may atomically create at most one immutable title operation after verified native and workspace cleanup. The operation uses the first input accepted by that native conversation, including an edit made before dispatch, and freezes the original Agent, harness/version, model/effort/tier, account/connection, provider, machine, device and Worker instance. It never reruns routing, uses workspace/session history or instructions, creates a conversation input, or claims the conversation workspace lease. A stopped/failed first turn does not queue a title.

If execution recovery later verifies the successful first turn, it may queue the pending title only when recovery completes on the exact original Worker device and instance. Recovery performed by another Worker settles the title as `skipped` with the `authority-lost` reason; title inference never transfers to a replacement.

`WatchAuxiliaryWork` uses its own request/response messages and `ReportWork` uses a capability-negotiated auxiliary lane, separate from ordinary `WatchWork`. A Worker owns at most one active title job; the server allows at most four active title jobs. Excess queued work remains durable. Old Workers receive no title assignment. Reconnect, Worker replacement or a new server epoch cannot transfer the original native attempt or resend its inference.

Paired Worker credentials may call only the endpoint-allowlisted Worker procedures, including `WatchAuxiliaryWork`. That handler independently requires the authenticated Worker to match the current machine and instance and to have negotiated automatic-title capability; the credential still cannot invoke owner product operations.

The Worker starts a fresh private Codex runtime in an empty title workspace with only fixed title instructions and the original first message. It excludes repository access, conversation history, templates, project instructions, ambient configuration, plugins, MCP, hooks and execution tools. It proves the exact effective model, provider, effort, service tier, work directory, read-only sandbox and approval policy, plus zero native request/stream retries before sending one response request through a fresh title-purpose relay grant. The relay reconstructs the exact model, prompt, instructions and no-tools request. Native inference is bounded to 30 seconds and 4 KiB of accumulated final-title text. Bounded Responses API reasoning events and items may pass through the pinned Codex adapter under a separate 256 KiB aggregate byte limit; the title Worker discards their contents and never treats them as title text or tool authority. Only trimmed, nonempty, single-line, NUL-free UTF-8 final text of at most 256 bytes is accepted; invalid output never triggers a repair call or silent truncation.

Before launching any native title verifier or inference process, the Worker durably records its one-time send intent and the server atomically claims the inference against the exact title job during execution registration. A lost response, crash or reconnect cannot send again. A retained result report can replay only under its original job/result identity. Title authority remains bound to the immutable initial execution ID, input, account, connection and configuration snapshot even when a queued follow-up changes the session's selected or active execution. The server still checks current account connection, project restrictions, original Worker/device/instance/server epoch, session existence, title generation/ownership, cancellation and lifetime budget before inference. A known matching-currency subtotal at threshold skips inference; incomplete evidence follows existing `ALLOW_INCOMPLETE` behavior.

Manual rename transfers title ownership atomically and advances its generation. A result is applied only while that generation still owns the automatic title; unrelated session revision changes do not invalidate it. Stop and Archive cancel title work; Archive remains pending until native cleanup is confirmed. Deletion and authority loss cancel owned work. Restore and Resume never create another title operation. Title failure, skip or uncertainty never changes conversation outcome, queue, or recovery, and a late result cannot recreate a deleted session or overwrite a manual title.

Authorized session metadata retains typed owner, mode, state and bounded reason: waiting, queued, running, succeeded, skipped, failed, unsupported or uncertain. An explicit `Unsupported` Worker result, including a changed or unavailable pinned profile, remains `unsupported` with the unsupported-profile reason rather than becoming an inference failure. The desktop exposes both state and its safe reason in session details and loaded sidebar rows, including their accessible descriptions. It never infers state from a local request or overwrites the status with cached selection. The new-session page requires server title capability, keeps its first-message draft when unavailable, and never asks users to invent a title.

## Storage

Automatic naming metadata lives in the existing session JSON document. The immutable auxiliary assignment is one durable UUID-v7 job. A one-time inference claim is stored independently from native credential grants. Schema migration adds a typed usage purpose without backfilling title work or repricing history; migration remains backup-first and transactional.

Title response usage uses purpose `session-title`, exact original session/project/execution/account/connection/provider/model/harness/version/thread/turn attribution and existing response-digest deduplication. Missing native usage remains unavailable and does not prevent applying a valid title. Title-purpose records do not enter conversation response counts or conversation missing-response coverage. Existing retained estimates, when available, contribute once to the session lifetime budget; no actual spend is inferred. Title work never overwrites conversation usage pointers.

## Security

The server does not call the provider directly. Only the original paired Worker can receive the assignment, and a completed conversation grant cannot authorize a title request. Title relay scope permits exactly one Responses operation and is bound to the immutable assignment. Execution registration rejects a second send claim under a new request ID; exact retries replay the original mutation receipt. A successful title report is accepted only after both the durable execution-registration send claim and the first title-relay HTTP request claim exist; typed pre-send failures may be reported without either claim. This prevents an assigned Worker from fabricating title text or usage without using the closed relay. Credentials remain in the existing protected Worker/server boundary; prompts, title text, provider responses, tokens and digests are excluded from logs.

The Worker uses a private temporary native home/work directory and joined process ownership. Cleanup must be confirmed before a successful title report. Uncertain process cleanup remains explicit and cannot trigger another native request. Fixed instructions, an effective read-only sandbox, no tools and disabled retry limits are all required; sandbox mode alone is insufficient.

## Logging

Structured records may include correlation/job/session/operation IDs, stable phases, capability outcome, usage-observed boolean and stable failure codes. Never log the first message, title, provider response, credential, request body, native config document or private path.

## Build and Test

Run `go test -race -p 1 ./cmds/delidev-cli/...` and `go vet ./cmds/delidev-cli/...`; run root Buf lint, breaking and generation-freshness checks after schema changes. Run `pnpm test` from `apps/delidev` after frontend changes. The opt-in `TestOptInManualCodexTitleProfile` accepts only `DELIDEV_CODEX_TITLE_EXECUTABLE`; it checks the exact installed version/native protocol and cleanup in a private temporary runtime without login, workspace access or inference. `TestOptInInstalledCodexTitleInference` uses that same explicit binary with one private loopback scripted-provider response and checks the installed app-server inference path, exact no-tools request, disabled native retries, original execution usage attribution and cleanup. Neither opt-in test uses a user account or external provider; the scripted fixture does not establish real provider/account billing or native desktop visual acceptance. Record installed-harness evidence separately from fixtures and platform evidence separately from cross-compilation.

## Dependencies and Integrations

- [Session acceptance and input queue](cmds-delidev-sessions-contract.md)
- [Native usage ledger and session budgets](cmds-delidev-usage-contract.md)
- [Native API relay](cmds-delidev-proxy-contract.md)
- [Native harness adapters](cmds-delidev-harness-contract.md)
- [v1 Connect protocol](protos-delidev-v1-contract.md)
- [TypeScript API client](packages-delidev-api-client-contract.md)
- [Desktop client](apps-delidev-desktop-contract.md)

## Change Triggers

Update the DeliDev project index, relevant Go/protocol/client/desktop `AGENTS.md` instructions, session/proxy/usage/protocol/client/desktop contracts, generated bindings, and evidence ledger whenever capability, ownership, usage purpose, UI, storage or native cleanup behavior changes.

## References

- [DeliDev project index](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Issue #1056: automatic session titles](https://github.com/delinoio/oss/issues/1056)
- [Issue #1057: chat-first session page](https://github.com/delinoio/oss/issues/1057)
