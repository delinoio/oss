# Missing shutdown observation review

Source: PR #1226 review `PRRT_kwDORRAKg86nqOXJ`, inspected at
`964d2981e1e81f071707b01d204f0027d79a8d77`.

A replacement could reconcile an original terminal's retained process owner while
missing shutdown metadata incorrectly asserted no output loss. Abrupt Worker
termination can abandon a suffix after a server-retained contiguous prefix.
Missing shutdown metadata now conservatively carries output loss. Original
unclaimed, prepared or claimed creation evidence excludes impossible output;
a finished, independently joined creation result preserves its own loss fact.
Neither loss nor missing metadata grants process cleanup authority.

Validation on the repaired source, 2026-09-30 UTC:

- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/worker -run
  'TestTerminal(MissingShutdown|Shutdown|Replacement|CloseRecovery|UncertainReport|ConfirmedCleanup)'
  -count=1`: passed, 2.932s. New cases cover running/started terminals,
  original pre-native phases, joined creation with/without loss, retained empty
  ownership and missing process evidence. Existing native Unix shutdown,
  replacement close and exact response-loss fixtures passed.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server -run
  'TestTerminalOutput' -count=1`: passed, 3.945s, including monotonic loss gaps
  with retained output and acknowledged cursors.

These are controlled fixtures. No abrupt production Worker kill, real remote
Worker, native Windows/Linux, desktop visual or release acceptance was executed.
The separate revocation-authority review remains pending; this repair does not
change device or native-work authority. Full final validation is recorded
separately for this maintenance pass.
