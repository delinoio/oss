# Handled-push audit presentation

The eighth maintenance pass inspected PR #1227 at
`caaa5dd44a8123c50220bcac69824298cf3b1e42`. Codex thread
`PRRT_kwDORRAKg86nveBU` correctly identified that a validated handled record's
execution ID and server handling timestamp were not displayed. The desktop
contract already required the original push audit.

The handled row now displays its original attempt ID, execution ID, verified
pushed commit and server transaction handling time. The timestamp is retained
verbatim in a `time` element; it is not the problem observation time or a
client-generated completion time. Existing validated handling reads and the
absence of Dismiss/Fix actions remain authoritative. This adds no query,
mutation, execution permission or provider-thread resolution.

Validation on the working changes:

- API-client build passed.
- The new component regression failed against the prior presentation because the
  execution ID was absent (`/tmp/delidev-1227-eighth-ui-red.log`).
- `pnpm exec vitest run src/pr-problems.test.tsx src/pr-fix.test.tsx --maxWorkers=1`
  passed both files and all 16 cases (19.81 seconds reported by Vitest). The new
  case verifies the complete original audit, a handling timestamp distinct from
  the observation, no new action and no mutation/collection side effect.
  Log: `/tmp/delidev-1227-eighth-ui-focused.log`.

The required complete frontend run is recorded separately when it completes.
These are component checks, not live-account/native desktop acceptance. Both
original security P1s and their pending human decisions remain unchanged. The
fixed audit thread is retained until the single final repair push succeeds.
