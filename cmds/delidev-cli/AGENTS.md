# DeliDev CLI

- Native read-only Sidechat additionally follows `docs/cmds-delidev-sidechat-contract.md`. Keep original account/snapshot provenance separate from the immutable child enforcement overlay and reference parent workspace roots without ownership. Parent deletion/storage cleanup must durably stop and join every dependent child before removing parent files; independent Fork lifetime remains unchanged.

- Execution-device user-facing messages follow issue #1136: use `Runner Device` / `Runner Devices` for former Execution Worker presentation nouns, including PR planning failures and schedule reconfiguration guidance. Preserve generic technical Worker terminology, Agent Worker, user-assigned names, CLI commands, structured logs, stable error codes/classifications, authorization, protocol/storage identifiers and all execution conditions. The desktop New session selector alone uses `Runs on`; follow `docs/apps-delidev-desktop-contract.md`.

## Scoped DeliDev ownership

Read the relevant owner before changing its behavior, including cross-domain consumers:

- `cmds/delidev-cli/internal/worker/AGENTS.md`
- `cmds/delidev-cli/internal/domain/AGENTS.md`
- `cmds/delidev-cli/internal/store/AGENTS.md`
- `cmds/delidev-cli/internal/cli/AGENTS.md`
- `cmds/delidev-cli/internal/server/AGENTS.md`

Record implementation status and validation results in pull requests, issues and CI logs/artifacts under the root DeliDev validation policy. Do not add repository evidence documents. Update instructions only when their rules or ownership change, not merely to record another validation run.

- Codex child ownership requires the original sender/receiver spawn evidence or a complete validated state-DB-only descendant read. A child `thread/started` notification may refine only an already proved exact parent relationship; unmatched starts remain discarded metadata and cannot allocate a product child or cleanup obligation.

- The native API relay in `internal/apiproxy` must join any started body/deadline cancellation callback before its HTTP handler returns. Downstream connection reuse cannot inherit a prior request's late deadline mutation; preserve upstream cancellation, once-only key reads and lease release under `docs/cmds-delidev-proxy-contract.md`.

Native session compaction for issues #1093, #1202 and #1203 follows the planned shared boundary in `docs/cmds-delidev-compaction-contract.md`. Its reservations must land on main before dependent implementation. Preserve original transcript/outcome, once-only native claims and independent history/cleanup verification; native acknowledgment never grants a successor checkpoint.

- Every Codex child collaboration, metadata notification and descendant-history snapshot publishes only output, observed model and usage actually supplied by that source. Retain last available values separately for later ownership comparisons; current-source omission cannot copy prior telemetry into an immutable receipt.

- Codex fork Local sharing is limited to original Local manifests with no parent-owned checkouts. Reject managed Worktree sharing before job acceptance and again before Worker native inspection/preparation and server publication; parent deletion retains those paths. Independent Worktree copying remains available. Follow `docs/cmds-delidev-forks-contract.md`.

- Fork workspace preparation validates every repository's manifest eligibility and the complete derived request before creating a child process index or launching Git. Definite manifest rejection must leave no new unpublished child scope; native HEAD/ownership failures retain their recovery classification and process evidence.

- Current-user service completion follows `docs/cmds-delidev-user-services-contract.md`: join the product controller and intent observer, acquire the bounded state gate independently of their canceled context, then atomically publish positive completion while retaining runtime ownership. Status readers and completion publication must share this gate, including Windows permission inspection.

- Browser ownership and cleanup follow `docs/cmds-delidev-browser-contract.md`. Keep bounded non-secret profile metadata on the original paired-client Device document with independent UUID-v7 identity/revision, exact authorization and receipts. Account deletion atomically marks every device obligation; session deletion and Archive retain profiles. Service-owned browser messages avoid reserved shared enum numbers and SQLite migrations; never persist browsing content or paths.

- Network bootstrap and native routing additionally read `internal/workernetwork/AGENTS.md`, `internal/nativeproxy/AGENTS.md` and `internal/outbound/AGENTS.md`. Go owns protected recipient/cache publication, authenticated generation admission and original runtime tunnel claims; native hosts grant only bounded OS/file presentation. Follow the network contract and keep capability reservations separate from product activation.
