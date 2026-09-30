# Uncertain close intent review repair

Review: <https://github.com/delinoio/oss/pull/1226#discussion_r4146992322>.
Parent revision: `aa10ea86a` (main Codex forks integration).

An accepted uncertain close previously cleared the only close request during
permanent deletion. Deleting sessions refuse new product controls, so workspace
removal remained blocked with no cleanup assignment for the original Worker.

The report transaction now replaces an uncertain close with a fresh close ID,
retaining the original machine/device/process owner, unconfirmed cleanup,
monotonic output-loss fact and deletion barrier. A confirmed cleanup alone
clears the intent. A fresh operation avoids reusing the Worker's immutable
finished uncertain result. Exact old report retries remain read-only and do not
replace the next close or regress a later confirmed outcome. Exact deletion
retries likewise preserve that pending close.

Uncertain close dispatch waits ten seconds after the latest terminal write,
including on reconnect, preventing the 100 ms watch loop from repeatedly
attempting unchanged ownership cleanup. Explicit authenticated close claims
remain available. No new shell, input, resize, workspace preparation or
cross-device cleanup authority is granted. Revoked-device cleanup still requires
the separately recorded unresolved authority decision.

Executed verification:

- Before the production fix, the new server regression failed in both original
  and replacement-instance cases at the missing/reused close-intent assertion.
- An intermediate post-change assertion exposed reuse of a decoded fixture
  struct, which retained omitted JSON fields. The fixture now decodes each
  resource into a fresh struct; safe state/UUID logging identified that issue.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server -run
  '^TestSessionDeletionReissuesUncertainTerminalClose$' -count=3`: passed
  (83.278 seconds). The authenticated fixture covers exact uncertain report
  and deletion replay, original and replacement same-device instances,
  delayed streamed reassignment, original-owner binding, withheld workspace
  deletion, eventual independently reported cleanup and loss retention.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/worker -run
  'TestTerminal(FreshCloseReconcilesAfterUncertainAcknowledgement|ReplacementResumesOriginalCloseJournal|ConfirmedCleanupRetiresOwnershipAfterAcknowledgement|UncertainReportPreservesProcessOwnership)$'
  -count=1`: passed (3.527 seconds). A held ownership-reconciliation lock
  produces uncertainty; lost acknowledgement retries preserve exact result
  bytes; releasing the lock permits a fresh close to reconcile the retained
  positive owner index on either the same or a replacement instance.
- `git diff --check`: passed.

Server cleanup reports are controlled authenticated facts, not installed native
PTY/ConPTY or remote-account evidence. The Worker retry fixture uses a positive
empty owner index and an exclusive lock; it does not infer cleanup from missing
PIDs or fabricate ownership. Full validation is recorded independently after
this repair commit.
