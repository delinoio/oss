# PR #1184: Grok retention-time explanation repair

On 2026-09-30, repaired Codex thread `PRRT_kwDORRAKg86nbn7U` after the separate
response-chart fix `022d2ca9`. Applied conditions now explicitly describe
response first-retention time. The negotiated Grok section explains that its
filters and daily buckets use the server's first retention of verified
completion after confirmed cleanup, matching the existing accounting row.

A mixed fixture retains the response on one day and the verified closed-input
unit on the next. Before the fix, it reproduced the misleading shared time-basis
copy. After the fix, app typechecking passed and
`pnpm --dir apps/delidev exec vitest run src/usage.test.tsx src/grok-accounting.test.tsx`
passed 2 files / 13 tests. The fixture verifies the distinct explanations and
original later-day native interval endpoints. These are component fixtures,
not new native desktop, provider, platform or release acceptance.
