# CLI workspace review baseline

The stable repaired aggregate run reported a failure in
`TestCLISessionAcceptanceQueueAndArchive`: stale-review submission received
`unavailable` from the workspace file reader (78.96 s test result; 220.718 s CLI
package result). No compaction operation was invoked in that test.

An isolated rerun with `GOMAXPROCS=2 go test -race -p 1
./cmds/delidev-cli/internal/cli -run
'^TestCLISessionAcceptanceQueueAndArchive$' -count=1` failed earlier at
`session review-context`, with the same workspace-reader `unavailable` class
(66.469 s package result).

An independent temporary source archive of untouched main `574c1a92c` retained its
exact Go module, DeliDev and generated protocol sources and repository license.
Running the same isolated command there failed at `session review create`, again
with workspace-reader `unavailable` (42.517 s package result). The temporary copy
used isolated test state and no checkout identity, credentials or environment files.

The failure predates this compaction change at the inspected main revision. The
different failure stages do not establish the cause of the reader's unavailability.
No read deadline, stale-review assertion or authorization requirement was weakened.
Neither the baseline result nor the passed native compaction scenarios establishes
a passing aggregate race suite; the complete stable run remains separately tracked.
