# Complete desktop child-page validation, PR #1225

Inspected base: `b5c14557d0652f42e63f709bbfaae78c5f1dc591`. Codex thread `PRRT_kwDORRAKg86nuayF` identified rendering malformed or foreign retained child documents as ordinary observations.

The desktop accepts a complete bounded page only after validating all resource envelopes, original session ownership, supported harness/version, closed child schema, product/native/execution/root identities, independent lifecycle status, partial output, ordered source coverage and exact native usage families/counter parity. Duplicate JSON keys, rounded/mismatched counters and inconsistent known-parent/cycle ownership reject the entire page. Inconsistent pages expose no child content and disable Next; parents on another page are allowed. Parsing original native numeric tokens into temporary decimal strings preserves exact integer checks without altering the displayed native report. Source coverage separately permits retained output/model/usage from an earlier supported report when the latest task observation omits it.

The red regression selection had 36 failures and two positive passes: malformed pages rendered or failed to show the required unavailable state. The repaired selection validates bad envelope/schema/UTF-8/size/key cases, source/sequence/output/usage cases, complete-page identity/cycle/bound cases, exact signed Codex counters, exact unsigned Claude task counters and retained nested Claude response usage. Older hierarchy/streaming fixtures now provide the actual complete retained shape rather than incomplete synthetic documents.

Focused verification uses the default Vitest deadlines:

```sh
pnpm exec vitest run src/subagent-page.test.tsx src/subagents.test.tsx src/session-subagents.test.tsx
pnpm typecheck
```

The final focused run passed all three files and 43 tests at default deadlines (14.55s); the fresh typecheck passed. The full required frontend command is recorded in the maintenance validation file after all repairs. Component fixtures do not prove actual native accounts, desktop packaging, signing, runtime cleanup or release acceptance. Historical evidence remains unchanged.
