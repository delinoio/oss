# Pinned native acceptance after review repairs

On 2026-09-30, code revision `957bc7bfffbe44ca8e18639f9743747147c20123`
includes main `574c1a92c` and the three first-review repairs. Used the official
disposable npm `@anthropic-ai/claude-code-darwin-arm64@2.1.236` artifact on macOS
arm64. Executable SHA-256:
`6bc4ba992d2786cbf0237c4453ca53c1fdf0c3b3d83ffa0025c0d8190ed27848`.
Provider traffic, server state and Worker/native runtimes were isolated; no user
credentials or configuration were used.

With `GOMAXPROCS=2` and the canonical disposable executable supplied through
`DELIDEV_NATIVE_CLAUDE_EXECUTABLE`, the unchanged private
`TestManualNativeExplicitCompaction/successful/continue-false` passed (10.436 s
package result). The public `TestManualNativePublicSessionCompaction` then passed
all four scenarios (340.661 s package result): success 96.89 s, provider rejection
88.22 s, insufficient history 76.77 s and cancellation 76.99 s.

The public fixture exercises authenticated acceptance and receipt replay, FIFO
blocking, independent compact status, missing utilization, native checkpoint
retention, explicit Resume after failure and continuation after a fresh Worker
instance. It verifies one original native/provider command, retained original
action evidence and no resend after canceled-action replacement. Checkpoint v2's
original claim proofs are consumed through the production restoration path.
Worker replacement waits for the real 45-second connection lease to expire;
production and fixture deadlines were unchanged.

These new successful runs supersede the earlier inability to reach compaction in
the separate acceptance-limit record; the earlier failures remain retained and
their cause is not established by this rerun. Evidence is specific to the pinned
macOS arm64 native binary with a scripted loopback provider. Hosted accounts,
subscription sessions, other platforms, release packaging and complete issue #964
acceptance remain unperformed.

After the merge and repairs, full `pnpm proto:check` against main `574c1a92c`, all
113 repository contract tests, generated-client typecheck and all 44 client tests,
and `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed. Client tests took 55.35 s;
contract tests took 12.032 s. The fresh aggregate race suite is recorded separately.
