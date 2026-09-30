# PR #1171: PR activity composition in session erasure

## Failure and source context

The 2026-09-30 CI run `36688363803` failed its Ubuntu, macOS and Windows server Go
jobs at `TestSessionDeletionRetiresSharedRemediationOperandsWithoutRefundingCounters`.
The tested merge was `bb0697ae7790969cd0d90b987fdaad05cf935a75`, combining PR head
`1ff315462337ecdece373f661591a6201df08bbe` with main
`faa7fbee6b6e2a1e77b45e169417c7de5c00530d`. The PR branch alone passed that test
20 times in non-race mode (8.048 seconds). An isolated Go/protocol source archive
of the exact CI merge reproduced its `recovery_required` failure (0.790 seconds).

Main's ordinary remediation-attempt writer now publishes PR activity on state
changes. Session purge called that writer for redaction from a cleanup transaction
without a business request identity, which failed activity actor validation.
Simply supplying an identity would also publish replacement activity during
erasure and leave pre-binding reservation activity outside the row session scope.

## Repair

Erase original attempt-source activity in bounded 200-record pages before removing
the attempt's session operands. Preserve UUID tombstones and ordinary deletion
events. Validate and update the shared attempt plus its typed index directly;
erasure does not publish a new PR business activity or invent a native outcome.
Original reservation provenance, lifetime counters and unrelated shared PR
activity remain retained. No schema, protocol or generated output changes are
needed. The PR branch and main are composed by the existing GitHub test merge;
no unrelated source is transplanted into the branch.

The regression adds complete metadata fixtures for original reserved/bound
activity and unrelated observed PR activity. It checks both original activity
removal and absence of replacement activity, while retaining the existing
operand-redaction, chain-counter and history-fingerprint assertions.

## Focused validation

Executed on 2026-09-30 with `GOCACHE=/tmp/delidev-1171-gocache GOMAXPROCS=4`:

- PR branch: `go test -race -p 2 ./cmds/delidev-cli/internal/store
  -run 'SessionDeletion|PRRemediation' -count=1` passed (63.444 seconds).
- Exact CI-merge source archive with the repaired deletion source/test files:
  `go test -race -p 2 ./cmds/delidev-cli/internal/store
  -run 'SessionDeletion|PRActivity|PRRemediation' -count=1` passed (47.608 seconds).

The CI-merge archive includes main's actual activity writer and validation.
These are isolated SQLite and protocol-source fixtures; they do not establish
real native/account/distribution acceptance or reclaimed physical space.
