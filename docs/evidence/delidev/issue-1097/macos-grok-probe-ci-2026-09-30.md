# macOS Grok discovery CI investigation

## Exact failure

The final PR #1218 repair inventory on 2026-09-30 reported macOS Go and aggregate
CI Result failures for published head
`d6d211596268c3eecef9d6c70eab2b695d2bf2eb`.
The failed job's `head_sha` was independently verified through the Actions API.
Logs were retrieved with `gh run view 36710570908 --repo delinoio/oss
--job 109871188069 --log-failed` from
[the macOS job](https://github.com/delinoio/oss/actions/runs/36710570908/job/109871188069).

The sole reported package failure was
`TestProbeOwnsBoundedInspectedInitialization/trailing` in the Grok harness.
It expected `Unsupported` for partial trailing ACP output but received
`Unavailable`. Its structured log identified the earlier `inspect` phase,
before the ACP trailing-output scenario. The subtest took 0.20 seconds; the
overall Grok package took 120.776 seconds. The log does not identify the precise
inspection failure cause. CLI, Codex/OpenCode harness, server, Worker and all
other listed packages passed in that job. The aggregate failure reflects this
macOS job, not a second independently identified defect.

## Bounded local verification

These commands ran from the repository root at merged source
`6f756e5fa2d5a816f6e844d0a319563f34fb4b8c`, without source or deadline changes:

- `GOMAXPROCS=2 go test -p 1 -timeout 3m
  ./cmds/delidev-cli/internal/harness/grok
  -run '^TestProbeOwnsBoundedInspectedInitialization/trailing$' -count=30`
  passed all 30 repetitions in 29.602 seconds.
- `GOMAXPROCS=2 go test -race -p 1 -timeout 4m
  ./cmds/delidev-cli/internal/harness/grok
  -run '^TestProbeOwnsBoundedInspectedInitialization$' -count=3`
  passed three repetitions of the complete probe group in 108.742 seconds.

The Grok probe and process implementation have no PR source delta from inspected
main. Configuration inspection, owned process input/exit handling and the
synthetic fixture were inspected, but these passing repetitions do not establish
the CI failure's root cause or prove it fixed. No speculative production or
fixture change is made. The already-validated main merge is published once;
its fresh CI results are checked on a later scheduled heartbeat. The preceding
CI failure remains visible until new evidence resolves it.
