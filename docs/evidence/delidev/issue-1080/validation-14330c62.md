# Final restore checks at 14330c62

Validated implementation `14330c62f297018aae485af3636fada6f323d4e0` on
2026-09-30, against freshly fetched main
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250`.

## Passing checks

- `go test -race ./cmds/delidev-cli/internal/store
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli
  -run 'Restore|BackupRestore' -count=1 -timeout 20m` passed all three packages:
  storage 321.565 s, server 36.978 s and CLI 19.790 s. Includes seven actual
  process-crash checkpoints, WAL consistency, exact receipts, concurrent restores,
  validation/publication failure preservation, changed recovery evidence,
  revocations, permanent deletion and staged migration.
- The four new permanent-deletion/remediation race cases passed separately
  (47.170 s), including shared history redaction without lifetime-counter refunds,
  unfinished deletion refusal, removal of settled temporary copies and retained
  uncertainty for unaccepted staging.
- `go vet ./cmds/delidev-cli/...` passed after the final source repair and again
  against the implementation commit.
- `pnpm proto:check` passed formatting, lint, breaking compatibility and exact
  generated freshness, using the repository's LFS-smudge-skipping baseline path.
- `GOMAXPROCS=2 pnpm --filter @delinoio/delidev-api-client test` passed all 46
  tests in five files, including the three real temporary Go-server cases.
  Earlier bounded build failures remain recorded in `restore-v3.md`; the final
  rerun followed explicit cache warming, without changing fixture deadlines.
- Client lint/typecheck and explicit build passed. Generated client `dist` was
  removed and the tracked worktree remained clean.
- `GOMAXPROCS=2 GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -p 2 ...`
  and the corresponding Linux arm64 command passed, using temporary outputs.
  These are compilation evidence, not Windows/Linux runtime acceptance.

## Complete race run and baseline comparison

`DELIDEV_NATIVE_USER_SERVICE_TEST=0 go test -race -p 2 -timeout 20m
./cmds/delidev-cli/...` remains running through the remaining native fixtures.
It already failed `TestCLISessionAcceptanceQueueAndArchive` at line 202 during
`session create --wait` with a bounded preparation timeout (67.18 s).
The CLI package completed with exit failure after 468.084 s. Passed packages
observed so far include API proxy, connections, credentials, domain, forwarding,
harness and Claude harness. A complete race pass is **not** claimed.

A separate current-head isolated run of that unchanged CLI fixture failed at
line 215, `session files read`, with workspace-reader `Unavailable` (57.954 s).
To compare the actual base, archived main's Go module, complete `cmds/` source and
generated Go protocol into an independent temporary directory, then ran the same
isolated race command there with `GOMAXPROCS=2`. The freshly fetched main fixture
also failed at **the same line 215 and workspace-reader `Unavailable`** (76.189 s).
The original base/current source and command are therefore independently tested;
this existing fixture failure is retained, without changing its deadline or
claiming the complete local suite passed. No real accounts or user state were used.

The continuing broad run's terminal outcome will receive a separate evidence
record. It is owned by this chat, with log `/tmp/delidev-1080-race-v3-final.log`
and execution session 94932; do not start an overlapping full validation.

Real account/harness, native Windows/Linux and release/distribution acceptance
remain unperformed. No frontend or Rust source changed, and historical evidence
and issue #964 gaps remain intact.
