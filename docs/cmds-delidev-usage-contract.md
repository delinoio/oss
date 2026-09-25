# DeliDev native usage ledger

## Scope
`cmds/delidev-cli/internal/domain`, `internal/harness/codex`, `internal/worker`, `internal/server`, and `internal/store` own the normalized response usage pipeline. Issue #964 requires DeliDev-only aggregation, the dashboard, attributable actual costs, separate historical token-price estimates and optional session budgets. The implemented boundary includes exact Codex response ingestion, durable deduplication, coherent read-only RPC/CLI aggregation and desktop presentation. Pricing, budgets and remaining native scopes remain required work.

## Runtime and Language
Go owns decoding, attribution, authorization and SQLite persistence. Native harness JSON is the only usage source. The relay, account catalogs and account-wide billing APIs cannot generate usage or establish spend.

## Users and Operators
Paired Workers publish observations for their exact accepted execution. The single-user server owns attribution and retained history. Owner/client reads use the bounded aggregation API below; neither the API nor internal ledger reader grants filesystem/database access.

## Interfaces and Contracts
Codex `0.151.0` emits `rawResponse/completed` for one upstream Responses completion, independently of cumulative `thread/tokenUsage/updated` snapshots. The pinned official protocol describes this exact completion as neither accumulated, estimated nor replayed. The closed adapter validates original thread/turn ownership and the exact response schema. It hashes the bounded provider response identity with SHA-256 before publication, retaining only lowercase 64-character digest identity. Foreign child threads remain private until a dedicated child adapter exists; unknown turns and late publication cannot acquire current execution authority.

The same pinned source exposes `experimentalRawEvents` only on `thread/start`. Its `ThreadResumeParams` and `thread/settings/update` have no equivalent, and a newly attached resumed listener explicitly disables these events. DeliDev omits the unsupported resume field instead of treating ignored input as enabled telemetry. Actual process-replacement FIFO/Resume tests retain the first known response subtotal and report subsequent accepted executions without exact response telemetry; they cannot sum replayed cumulative snapshots to invent coverage. Complete resumed exact usage requires a separately verified native capability/profile or retained native-connection lifecycle. This limitation also applies to raw question/permission acceptance after process replacement; canonical closure alone remains insufficient.

The normalized `response-usage` observation becomes `WorkerService.PublishExecution`'s `response-usage-observed` document under the existing durable Worker outbox, contiguous event sequence, original request receipt and current owning Worker checks. One observation UUID accompanies its response digest, nullable native counts and closed cost-evidence classification. The server supplies original session/project/execution/account/connection/provider/model/harness/version/native thread/turn attribution from the immutable assignment. Later routing, configuration removal or account edits cannot relabel these records. `session.execution.latest_response_usage_id` refers to the retained exact response; `latest_usage_id` remains the separate cumulative observation reference.

Required counters in a present native usage object retain the pinned input/output/cache/reasoning/total meanings. Cached input and reasoning output are subsets, not additional total tokens. Unreported cache-write counts remain null. A missing/null usage object remains unavailable, never measured zero. Partial malformed native objects reject publication rather than filling missing fields. Cumulative and last-request context observations remain immutable but cannot be added to this ledger or backfilled as exact responses.

`usageMetadata.amount` has no authoritative currency or billing semantics in this pinned contract. The adapter validates its bounded native presence, discards its value, and retains only `missing` or `unspecified-native-metadata`. Neither is an actual-cost amount, token-price estimate, currency or subscription charge. Unknown actual cost is unavailable.

### Read-only aggregation
`UsageService.GetUsageSummary` and `delidev usage summary` are owner/paired-client-only. Existing `usage list/get` cumulative observation reads remain available. Filters use original session/project/account/provider/model UUIDs or projectless General Chat, with a half-open first-retention-time range. Omitted bounds default to server now and 30 days before the upper bound; explicit ranges are positive, at most 366 days and within the supported calendar range. CLI timestamps are RFC3339 with explicit timezone.

One authorized two-second SQLite read transaction computes overall and per-session/account/provider/model groups from unique exact response rows, including Archive. No current configuration is required to retain those original identities. Current display labels are optional metadata from the same snapshot; renamed/deleted labels cannot change original UUID attribution. Each input/output/cache/reasoning/total measure contains an exact decimal-string known subtotal, measured-response count and unavailable-response count. Empty subtotal is unavailable; `"0"` is measured zero. Arbitrary-precision addition preserves values beyond JavaScript and signed 64-bit limits; overlapping categories are never added to total.

Every response explicitly declares observed-root-only incomplete coverage and unavailable actual/estimated costs. The independent coverage count reports execution jobs accepted within the same time/filter range without a retained exact response in that range; this includes pre-native failures and unobservable resumed execution and never means zero calls. Missing child/legacy/native telemetry cannot become verified completeness. Bounds are 25,000 responses, 500 groups, 10,000 accepted executions and 2 MiB in either protobuf or JSON encoding. Capacity/timeout failures return guidance to narrow the filters and no partial summary.

The desktop uses generated Connect Query directly, defaults to the server's last 30 days, retains unapplied filter drafts across navigation, distinguishes General Chat and preserves decimal precision with BigInt. It shows known subtotals, missing-response coverage, separate unavailable costs, current display names with original identities and links to retained sessions. A refresh error marks retained data as stale. Reading neither changes inbox/archive/session state nor starts work; pricing and budget configuration remain pending.

## Storage
Schema 14 adds `response_usage` with one UUID-v7 record and a unique `(account_id, provider_id, response_digest)` key. The response body is bounded to 16 KiB. First publication commits the ledger, session progress, event and receipt in one transaction. An exact repeated response under another publication sequence/observation UUID returns the original ID without changing its timestamp, sequence, attribution or counts. A reused observation UUID or native identity with changed evidence requires recovery and rolls back the complete publication; it cannot double charge, silently upgrade missing counters or move inherited history to a new execution/turn/session. Reopening the server preserves this identity check.

The server's first-commit timestamp is `observed_at`; it is not represented as a provider timestamp. Time/session/project indexes support bounded aggregation. Deleting the owning session cascades its derived ledger records. Archive and configuration deletion retain history. Permanent deletion and managed-backup erasure remain part of the separate full-issue cleanup contract.

Migration from schemas 1–13 first publishes a validated, synchronized original backup, then changes schema atomically. Schema-13 migration does not rerun search or earlier ownership migrations. No legacy cumulative usage is converted into exact response records. Schema conflicts retain the original database and backup without resetting history.

## Security
Publication retains all existing session/input/job/account/connection/Worker and terminal checks. Counter or identity conflicts cannot consume an event sequence. Raw provider response IDs, native metadata amounts, instructions and content never enter normalized usage, journals or logs. This path needs no additional reporting credential and never authorizes inference or changes account health, pause, outcome or queue capacity.

## Logging
Read completion uses `usage_summary_read_completed` with correlation ID and bounded group/response counts, never filter values, labels or token/cost totals. Structured `execution_event_committed` logs include the closed response-usage event kind, job/execution UUIDs, sequence and receipt replay classification. Exclude raw native bodies, response digests, amounts and token counters. Typed failures retain original ownership without logging provider diagnostics.

## Build and Test
Run package Go tests and vet from `cmds/delidev-cli`. Ordinary tests cover exact nullable counts, closed schema/privacy, ownership, duplicate identities, conflicting evidence, rollback, restart, session deletion and schema-13 backup/migration failure. Ordinary tests never run installed harnesses. Opt-in `TestManualNativeCodexUsesRegisteredServerRelay` accepts an explicit pinned executable and verifies the real structured response through the Worker outbox and server ledger using a private runtime and scripted loopback provider; it cannot establish hosted billing, subscription, child, or other-platform evidence.

## Dependencies and Integrations
- [Native harness contract](cmds-delidev-harness-contract.md)
- [Session and durable event contract](cmds-delidev-sessions-contract.md)
- [Connect protocol](protos-delidev-v1-contract.md)
- [Evidence ledger](cmds-delidev-evidence.md)

## Change Triggers
Update this contract, the project index, protocol/session/harness docs, evidence ledger and `cmds/delidev-cli/AGENTS.md` when usage scope, deduplication, pricing, cost evidence, migrations or dashboard interfaces change. Do not promote fixture evidence into actual billing or cross-platform acceptance.

## References
- [DeliDev project](project-delidev.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Pinned official response event semantics](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/protocol/src/protocol.rs)
- [Pinned official raw response schema](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server-protocol/schema/json/v2/RawResponseCompletedNotification.json)
- [Pinned official event conversion](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server/src/bespoke_event_handling.rs)
- [Pinned resume listener behavior](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server/src/request_processors/thread_processor.rs)
- [Pinned thread request types](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server-protocol/src/protocol/v2/thread.rs)
- [Pinned cumulative token replay](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server/src/request_processors/token_usage_replay.rs)
