# Issue #1204: original OpenCode event-stream reconciliation

## Revision and scope

- Issue: <https://github.com/delinoio/oss/issues/1204>.
- Baseline: `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`, freshly fetched `main` before implementation.
- Implementation revision: the enclosing Git commit of this record, on `kdy1/issue-1204-opencode-stream-reconciliation`.
- Validation host: macOS arm64, Go `1.26.8`, pnpm `10.26.2`, pinned OpenCode `1.18.32`.
- No protocol fields, migration versions, generated bindings or public client shapes changed. The historical evidence ledger remains unchanged.

The harness now admits one bounded reconciliation after an original transport failure, revalidating the original live process, runtime/workspace directories, authenticated authority, effective configuration and session. It registers the replacement listener before reading two matching complete idle snapshots. Joining occurs on a private observer copy; snapshot proofs retain empty native event IDs, and failed joining publishes no facts. Original input/reply claims and positive acceptance evidence remain authoritative. Stop and owned closure cancel admission and join in-progress reading.

The positive profile covers completed original text/reasoning and usage, monotonic completion of known tools, and original assistant successors. Current facts are published through existing Worker transcript/tool/usage adapters and the durable outbox. Unsettled histories, missed tool approval/running boundaries, unowned interactions, unsupported event-only observations and uncertain answers remain recovery-required. Independent history checks also validate late queued observations against the recovered facts before public terminal publication. Cleanup remains separately verified.

## Deterministic checks

The following commands were executed from the repository root:

```sh
go test -race ./cmds/delidev-cli/internal/harness/opencode -run 'Test(Event|Input|History|OwnedAPI|Interaction|Original)' -count=1
go test -race ./cmds/delidev-cli/internal/harness/opencode -run '^TestEventReconciliation' -count=1
go test -race ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/domain -run OpenCode -count=1
go vet ./cmds/delidev-cli/...
pnpm --filter @delinoio/delidev-api-client build
pnpm --filter @delinoio/delidev-api-client lint
pnpm --filter @delinoio/delidev-api-client typecheck
pnpm --filter @delinoio/delidev-api-client test
git diff --check
```

Focused harness and OpenCode Worker/server/domain race checks passed. The store selection reported **no tests to run**; this is compilation, not store-test acceptance. The client build, lint and typecheck passed, and its four test files passed all 44 tests. Vet and whitespace checks passed.

New harness fixtures verify:

- A retained text prefix grows to complete original text without a new native input, event identity or inference authority; independently stored history matches afterward.
- Buffered message/text snapshots overlap without duplicate parts, retaining real buffered event IDs. An original running Read call completes across its original assistant successor with one call identity, including a matching buffered tool update.
- A positively accepted original answer retains its receipt; an HTTP-acknowledged answer whose pending request disappears remains unconfirmed. Neither case consumes another claim or POST.
- Changed original owner/configuration, foreign/changed/reordered/missing/duplicate history and oversized responses refuse without partial facts or a second cycle.
- Nontransport failures cannot reopen. An expired original 30-second deadline cannot renew, and continuously changing snapshots exhaust its remaining time without changing the published prefix.
- Cancellation joins the active read, a Stop preceding cancellation registration denies later admission, and an unrelated owned session remains usable.
- Private read proofs survive Freeze/Thaw while callers cannot invent snapshot provenance. Progress cannot settle before the entire verified publication batch drains. A late covered heartbeat is retained; a late unsupported event blocks history/terminal proof.

Existing Worker/server regressions exercise monotonic text/tool ownership, overlapping step/final-message usage without invented totals, exact durable outbox acknowledgment replay, original interaction claims, terminal failure classification, Stop/revocation and completed-checkpoint recovery. These are fixture/public-validation results, not a new native end-to-end outbox acceptance claim.

## Pinned native stream-loss check

The opt-in `TestManualNativeOpenCodeEventReconciliation` was executed with an isolated temporary OpenCode `1.18.32` executable and a scripted loopback Chat Completions provider. Its command is:

```sh
DELIDEV_NATIVE_OPENCODE_EXECUTABLE="$native_executable" go test ./cmds/delidev-cli/internal/harness/opencode -run '^TestManualNativeOpenCodeEventReconciliation$' -count=1
```

This uses a real original native process and event endpoint. After an accepted text delta, the fixture closes only that response body, releases the original provider request, and verifies complete text, unchanged session identity, exactly one provider request and exactly two original claims (creation and input). It then independently verifies stored history, completed cleanup and original process reconciliation. No hosted account, installed user configuration or real external model is used.

The latest executed native run passed. An earlier race-instrumented native run also passed before the final runtime-directory and history-tail guards. Two subsequent race-instrumented attempts failed during startup, before creating the session or exercising reconciliation: one returned initialization/cleanup recovery-required, and one exhausted configuration initialization. Host load was high during those attempts; their cause was not independently established. The final native result must not be described as repeated race-instrumented native acceptance.

## Qualifications

A broader non-race harness/Worker attempt encountered failures in unchanged `TestProbeOwnsAuthenticatedServerAndCleanup` startup/cleanup fixtures (duplicate, trailing, timeout, unauthenticated, configuration and schema cases). This record does not claim that the entire repository or complete Go suite passed, or that those failures were established baseline failures.

Native evidence covers macOS arm64 and one scripted text-prefix transport loss. Tool/reply overlap, changing-state deadlines, cancellation races and public outbox/usage behavior are deterministic fixtures and existing public validation. Windows/Linux native stream-loss, hosted-account inference and native end-to-end Worker/server outbox replay were not executed. The supported bounded profile can refuse rather than recover when current facts cannot close missed event-only or acceptance evidence. No exactly-once transport guarantee, Worker-restart adoption, automatic Resume or platform-wide acceptance is claimed.
