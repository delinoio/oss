# Terminal control focus review

Source: PR #1226 review `PRRT_kwDORRAKg86nswzj`, inspected at
`964d2981e1e81f071707b01d204f0027d79a8d77`.

The one-time focus marker remained set when a pending control disabled the
textarea and browser focus moved away. Input now focuses whenever its blocked
state transitions to available. The effect depends only on that boolean, so
ordinary metadata refresh does not steal focus or replay a control.

The delayed authenticated Connect component fixture covers both input and
resize, moves focus away while the textarea is disabled, then checks that
acknowledgement restores focus with exactly one control request. It also checks
that a later metadata refresh preserves focus on another control. Both cases
failed the final focus assertion before the repair. The first fixture attempt
exposed jsdom's disabled blur behavior; the corrected fixture explicitly moves
focus to another enabled control to model browser focus loss.

Repaired-source validation, 2026-09-30 UTC:

- Generated API client build passed.
- `GOMAXPROCS=2 pnpm typecheck` in `apps/delidev`: passed.
- `GOMAXPROCS=2 pnpm exec vitest run src/session-terminals.test.tsx
  --maxWorkers=2`: passed all seven tests.

This is component/Connect transport evidence, not native desktop visual
acceptance. The required full desktop test command is recorded separately in
this pass's final validation evidence.
