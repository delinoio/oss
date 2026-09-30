# Replacement close journal review repair

The repair starts from merged revision `593034acb6ee292ccf81a0d8885cdaa5366ae1f5`
and addresses PR #1226 thread `PRRT_kwDORRAKg86njO2b` on 2026-09-30.
The original journal rejected another Worker instance before the server could
grant its existing close-only reconciliation authority, leaving an interrupted
close identity pending across restart.

The Worker now validates the original close operation/digest and retains its
original instance, claim/report IDs and finished result. Separate synchronized
close-recovery metadata binds fresh claim/report IDs to the replacement instance.
Claim response loss repeats that same claim; confirmed current authority permits
only original-owner close reconciliation or the unchanged finished report. A
further replacement must claim new authority and uses new instance-bound receipt
IDs without changing native result bytes. The auxiliary report loop requires
confirmed current-instance close authority. Interrupted started closes can
reconcile their original process owner, while create/input/resize remain excluded
from replacement adoption and native replay. Missing ownership stays uncertain.

Verification:

- Before the fix, `GOMAXPROCS=2 go test -race
  ./cmds/delidev-cli/internal/worker -run '^TestTerminalReplacement' -count=1`
  failed all four close phase cases (prepared/claimed/started/finished) with the
  original journal instance rejection (1.469 s).
- After implementation, the complete focused terminal Worker suite passed once
  (13.838 s), then `GOMAXPROCS=2 go test -race
  ./cmds/delidev-cli/internal/worker -run '^TestTerminal' -count=3` passed
  (22.652 s). It includes all four interruption phases, claim/report response
  loss, unchanged original identities/results, auxiliary retry, successive
  replacement instances, missing-owner uncertainty and rejection of non-close
  or changed journals. Existing native Unix creation/input receipt-loss and
  failed-control cleanup fixtures also ran.

The new recovery fixtures use an authenticated-shaped Connect test transport and
private process-owner evidence; they do not represent installed Windows ConPTY,
physical remote Worker or release acceptance. Broader validation is recorded
separately. Historical evidence remains unchanged.
