# Background-requested failed-history review repair

## Source and behavior — 2026-09-30

Repair base: `16b70e5efdd2edb79918d785cdfdd9fe1892016f`, PR #1166.
This record accompanies the repair for review thread
`PRRT_kwDORRAKg86na_yG`.

A valid `background_requested` terminal with no tracked background-task event
could previously satisfy the failed continuation candidate. The shared domain
gate now rejects that terminal independently of task publication. Regression
fixtures retain valid failed terminal evidence and an empty task inventory;
the Worker keeps the v1 completion and the server refuses v2 continuation proof.
Ordinary verified failures and successful roots retain their existing behavior.

## Executed validation

Passed with task-private temporary Go module/build caches:

`GOFLAGS=-p=1 GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/server -run 'TestClaudeFailed(ContinuationRequiresCorrelatedNonAbortedSettledInput|CheckpointStillRequiresOriginalNativeEOFProof)$|TestClaudeContinuationRequiresOriginalSettledPermissionAndReport' -count=1`.

Domain, Worker and server packages passed in 3.389, 3.710 and 24.399 seconds.
`git diff --check` passed. Earlier attempts using the shared Go cache failed
because compiler/vet and dependency source files disappeared during the run;
those attempts are not passing validation.

These are temporary native/controller/server fixtures. They establish no new
installed-Claude, hosted-account, desktop-platform or release acceptance.
