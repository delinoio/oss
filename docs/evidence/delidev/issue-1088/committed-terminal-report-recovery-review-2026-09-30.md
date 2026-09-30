# Committed terminal report recovery review

Source: PR #1226 review `PRRT_kwDORRAKg86nqOXP`, inspected after
`97904a909` (the independent missing-shutdown-loss repair).

A finished report whose acknowledgement was lost remained on disk after Worker
replacement. Committed cleanup removed its assignment, but auxiliary observation
skipped the previous instance's journal. The existing `ReportTerminal` RPC now
recognizes only the exact accepted receipt under current same-device/machine
and live terminal-capable Worker authority. Replacement or purged-record
acknowledgements return no terminal resource. Absent receipts cannot mutate;
new reports retain original current-instance authority. No protobuf numbers,
new RPC, new device authority or native replay are introduced.

Report receipts bind only their closed kind and original terminal/machine/device
UUIDs. Entity deletion and session purge rebuild that allowlist and redact all
other receipt fields. Legacy unbound receipts still require the original record.
The Worker retains original report instance, request ID, operation and result
bytes, synchronizes acknowledgement, then independently retires local ownership.
A scan retains its directory position through bounded 4,096-entry batches, so
an older backlog cannot disable every later acknowledgement/retirement scan.

Validation on the repaired source, 2026-09-30 UTC:

- Race-enabled server and Worker focused tests using `GOMAXPROCS=2 go test
  -race -p 2 ./cmds/delidev-cli/internal/server
  ./cmds/delidev-cli/internal/worker -run
  'TestTerminal(Replacement|RetirementScan|CloseRecovery|ConfirmedCleanup|Journal|UncertainReport|Report|Receipts)|TestSessionDeletionReissuesUncertainTerminalClose'
  -count=1`: passed, server 31.340s, Worker 4.942s.
- Additional current server ownership checks with `-run
  'TestTerminalReplacementReadsOnlyCommittedOriginalReportReceipt' -count=1`:
  passed, 4.528s. Exact acknowledgements succeed with retained/purged records;
  expired leases, changed bytes, absent receipts, another machine, new reporting
  identities and revoked credentials fail without restoring native authority.
- Store tests with `-run
  'TestTerminal|TestSessionDeletion.*(Receipt|Replay|Redact)' -count=1`: passed,
  3.458s. Direct terminal deletion and session purge retain only the four
  acknowledgement metadata fields; extra and ordinary receipt content is removed.
- Worker fixtures cover lost acknowledgements for close/spontaneous exit,
  replacement of close recovery, a held retirement lock, another local retry
  without RPC, immutable report bytes and an oversized malformed backlog.

Intermediate tests caught the old refusal expectation, startup's independent
uncertainty write, generic purge receipt redaction, and a missing fixture report
UUID. The fixtures now compare current post-start state and retain valid original
identities; no native timing limit or cleanup assertion was weakened.

These are controlled protocol/storage/ownership fixtures, not real remote Worker,
native Windows/Linux or release acceptance. The revocation-authority decision is
still pending; revoked credentials remain unusable. Full final validation is
recorded separately for this pass.
