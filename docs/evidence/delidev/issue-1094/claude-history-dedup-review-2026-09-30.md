# Claude repeated child-history inspection repair

Review thread: `PRRT_kwDORRAKg86nkPEe`, reviewed head `02d557b18`.

The finding is actionable. The root-idle read could publish terminal child A
while B remained live, and the pending-terminal read could publish A's identical
native leaf again after B settled. The repair deduplicates the verified
child/leaf/projected-telemetry digest in a bounded execution-local set, committed
only with receipt acknowledgment. Inspection still independently revalidates
the complete transcript and current original sidecar before skipping a duplicate.
New leaves and actual native finalized telemetry changes remain admissible;
unrelated native file/sidecar changes cannot create another identical receipt.

`GOMAXPROCS=4 go test -race ./cmds/delidev-cli/internal/worker -run
'SubagentClaudeHistory|SubagentClaudeIdle|ChildTask|ChildHistory' -count=1
-timeout=5m` passed (4.443 seconds). The regression uses real private temporary
child JSONL/sidecar files and two acknowledged original Agent tasks. It verifies
lost-ack exact receipt replay without premature identity installation, A inspected
while B is live, only B publishing after later settlement, repeated all-closed
inspection, a new leaf, usage finalization and deduplication after finalization.
Original idle/terminal authority remains covered separately. Owning instructions
and the subagent contract were updated together.

This fixture result does not establish real-account/native-platform acceptance.
The thread is resolved only after the single final repair push succeeds.
