# Manual-fix preflight retry backoff

Date: 2026-10-01 (Asia/Seoul); 2026-09-30 UTC.
PR: [#1227](https://github.com/delinoio/oss/pull/1227).
Review: [preflight retry P1](https://github.com/delinoio/oss/pull/1227#discussion_r4148207864).
Revision: the enclosing repair commit, following merge `089bb124a`.

The finding is valid. A failed remote preflight publishes a stable dispatch block,
but blocked sessions with retained queued input remain execution candidates. The
four-session lane previously removed completed ownership without retaining a
retry deadline, permitting unchanged failures to read GitHub on each one-second
scan. This is distinct from an accepted native execution or push replay.

The coordinator now retains scheduler-local monotonic retry state. Failed
unaccepted attempts wait 30 seconds, then double to at most five minutes. The
failure block's own revision, and unrelated edits while blocked, do not reset the
deadline. Explicit Stop/Resume or a fresh accepted ready input clears scheduling
delay while retaining all original remote, configuration, queued-input and atomic
claim gates. Successful dispatch removes the entry. Abandoned entries expire
after a further five minutes; four admissions per second bound retained state.
A fresh server epoch may inspect an original candidate again. This introduces
no persisted schema, account choice, native replay or security grant.

The four-session cancellable lane, ordinary five-second dispatch bound and
shutdown join remain. Structured logs record session identity and retry delay
only, alongside the existing stable error-code event. Prompts, provider output,
configuration, credentials and private paths are not logged.

## Executed regression verification

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server
-run 'TestManualPRFixDispatchRetry|TestManualPRFixRPCExactAcceptanceReplayAndPausedExclusion/ci'
-count=1 -timeout=5m` passed (8.451 seconds).

Deterministic deadline tests cover every intervening one-second tick, exponential
growth/cap, independent candidates, abandoned-state expiry and failure-block
revision changes. The authenticated accepted-fix fixture runs the real dispatch
coordinator against an unavailable provider: one read across unchanged ticks,
another after the deadline, and a fresh read after explicit Stop/Resume. The
original fix input remains queued and has no native assignment; an unrelated
ordinary session dispatches while the manual read fails. A deliberately stalled
provider read is canceled and joined before shutdown returns. Scheduler
readiness is simulated with an intentionally incomplete workspace manifest,
which cannot supply native execution proof if the remote gate is skipped.

Broader merged-tree validation is recorded independently. This focused result
does not establish a full-suite, native/account/platform or release pass. The
original Git executable/configuration and companion-content P1s remain unresolved
and their separate human decisions remain unanswered. No documented full-access
or companion repository support is removed or described as an isolation boundary.
