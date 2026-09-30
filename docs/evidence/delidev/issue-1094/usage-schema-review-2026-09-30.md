# Native child usage schema and counter-parity repair

Review thread: `PRRT_kwDORRAKg86nkPEp`, reviewed head `02d557b18`.

The finding is actionable. An arbitrary JSON object previously passed shared
child-usage validation, and normalized counters were not compared with their
native report. The repair requires a bounded original report for supplied usage,
closes it to the source-specific Codex cumulative, Claude task or Claude provider
schema, and requires exact scope and nullable normalized counter parity before
accepting any batch member. Unknown/case-aliased/duplicate fields and invalid
native counters are rejected. Original JSON bytes remain unchanged; a temporary
Claude numeric-counter conversion reuses the existing shared provider schema.
Unavailable usage remains nil and the reports remain non-additive.

The new domain regression failed against the prior implementation with 47
malformed/mismatched-report failures before the fix. After the fix,
`GOMAXPROCS=4 go test -race -p 2 ./cmds/delidev-cli/internal/domain
./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker
./cmds/delidev-cli/internal/harness/claude
./cmds/delidev-cli/internal/harness/codex -run
'Subagent|ChildHistory|ChildTask|ClaudeUsage|ProviderUsage' -count=1 -timeout=5m`
passed all five packages (domain 1.628 s, server 14.394 s, Worker 17.143 s,
Claude 2.330 s, Codex 2.882 s). The full domain race suite also passed after
expanding the Claude task/provider projection regression cases; its actual
package result is retained in the final pass validation record.

Coverage includes uint64 task and int64 provider bounds, measured zero versus
unavailable, exact report byte retention, complete nested provider metadata,
unknown secret/diagnostic extensions, case aliases, duplicate keys, null/array
reports, wrong source/scope, missing reports, mismatched/invented/omitted counters,
negative/fractional/exponent/out-of-range native counters, required task fields
and Codex cumulative/last/context validation. Server regressions reject a mixed
valid/invalid child batch without retaining either child or advancing original
execution ownership, then accept and replay the original corrected report
without duplicating its source coverage. Owning instructions, the subagent
contract and the project cross-domain invariant were updated in the same change.

Focused fixture verification is not native-account/platform acceptance or a
completed broad repository validation. The thread is resolved only after the
single final repair push succeeds.
