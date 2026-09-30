# PR #1184: response chart model inventory repair

On 2026-09-30, repaired Codex thread `PRRT_kwDORRAKg86nbn7R` from PR head
`babbabb653dd16f8bca95eac14b8792729e5f747`. The response-chart projection
now includes only models with retained responses. The original complete
snapshot still supplies the Grok model tables; response models with unavailable
counters remain visible. Calendar intervals, server ranking and server-provided
Other totals are preserved without renderer aggregation.

Before the fix, both Grok-only and mixed/unmeasured-response fixtures reproduced
the native-only row in chart View data. After the fix, app typechecking passed
and `pnpm --dir apps/delidev exec vitest run src/usage.test.tsx src/grok-accounting.test.tsx`
passed 2 files / 12 tests. Fixtures also verify that the Grok model table retains
its original model and that rendering leaves the complete inventory intact.
These component results do not establish native desktop or provider acceptance.
