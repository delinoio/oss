# PR #1224: authorize fork job and child observation

The Codex review at
<https://github.com/delinoio/oss/pull/1224#discussion_r4145945764>
identified that `GetSessionFork` read jobs and published children without the
owner/client authorization used by fork acceptance. Worker credentials could
therefore retrieve client-only fork observations by job ID.

The shared observation helper now calls `RequireForkActor` inside its read
transaction before looking up a job. This also applies to acceptance-receipt
reads and rechecks the current paired-device record. Missing principals and
Workers are rejected, and a stale revoked client cannot retrieve either a queued
job or a published child.

Validation on macOS arm64:

- `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/server -run 'SessionFork' -count=1 -timeout=5m`
  passed (33.777s).
- The new regression exercises the authenticated public owner and Worker RPCs
  for both queued jobs and published children. Workers receive `PermissionDenied`
  for existing and missing job IDs, demonstrating authorization precedes job
  existence disclosure. Current paired clients can observe the results; missing
  and revoked principals are rejected at the service boundary. Source bytes and
  revision remain unchanged.
- An initial run failed because the revoked-client assertion expected
  `Unauthenticated`; the existing `RequireForkActor` policy returns
  `PermissionDenied`. The assertion was corrected to the established policy.

This is controlled server authorization evidence, not a new installed native
agent or packaged desktop run. Fresh pushed-head CI and review remain separate.
The historical evidence ledger is unchanged.
