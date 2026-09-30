# PR #1212: stream-termination fixture repair

## Failure and inspected source

On 2026-09-30, maintenance of PR #1212 at
`585d54a399e2cbe25285df7022a5ca5f99bf5130` found the Ubuntu Go job failing
`TestStreamTerminationCancelsRunningOwnedWork/deadline_exceeded` with
`stream termination left native work running`. The completed job log was read
through the GitHub job-log API while the overall run remained in progress:
[CI job](https://github.com/delinoio/oss/actions/runs/36699980405/job/109837418122).

The fixture allowed five seconds for both heartbeat expiry and joined native
cleanup. Its heartbeat case uses three seconds of silence, while the Unix process
controller permits ten seconds for termination before reporting uncertainty.
The fixture also omitted heartbeats during native startup and could delete its
temporary state before joining the Worker on a startup failure. Runtime stream
and process ownership behavior is unchanged by this repair.

## Repair

The controlled server emits heartbeats during supervisor startup. After the test
observes the original native-start marker, it explicitly returns the selected
stream error/EOF or stops emitting heartbeats for the silence case. Startup has a
20-second guard; completion has a separate 30-second guard under a one-minute
parent context. Every exit cancels and joins the Worker before server/temporary
state teardown. Early termination reports only its sanitized typed failure code.

The original assertions remain: exact stream failure classification, no success
report after disconnection, and the original finished job journal containing
`canceled` with no output. Waiting longer cannot substitute for those facts.

## Validation

- Before the repair, the three-run focused macOS race command failed two runs,
  reproducing the completion/startup timing failures; one run passed.
- After the synchronization/timing repair,
  `go test -race ./cmds/delidev-cli/internal/worker -run '^TestStreamTerminationCancelsRunningOwnedWork$' -count=3 -v`
  passed all three runs and all twelve subcases. Individual valid subcases took
  up to 9.32 seconds, above the previous five-second guard.
- `go vet ./cmds/delidev-cli/...`: passed.
- Required administrator and async-commit-hook embedded asset builds: passed.
- After the final early-failure diagnostic edit, the focused race command with
  `-count=1` passed all four subcases (13.201 seconds), and Go vet passed again.
- All six DeliDev protocol/structure script tests passed.
- The first `go test -race ./cmds/delidev-cli/...` attempt did not pass: existing
  Worker and workspace fixtures failed or reached the default ten-minute package
  limit. Other Go suites were active on the same host, and system load was high;
  this is possible contributing context, not proof of their root cause. No
  complete local suite pass is claimed.
- A serialized `go test -race -p=1 -timeout=20m ./cmds/delidev-cli/...` rerun is
  in progress; its result remains pending.

## Limits

This is a controlled native-process fixture repair on macOS. Repaired Linux CI
acceptance requires a fresh check on the pushed head. No installed Codex, model
discovery, provider account or subscription acceptance was exercised. PR #1212
remains the reservation prerequisite; issue #1206 remains unimplemented and open.
