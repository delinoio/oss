# PR #1182 main integration maintenance

Date: 2026-09-30. PR head before repair: `39a420750553426a7b090aa5e65d213573691645`. Merged main: `126641a6dcf274420c8c5800a6e88079f0e19990`. Host: macOS arm64. The existing managed checkout was clean, on the exact PR branch, and matched the remote head before repair.

The desktop `AGENTS.md` conflict is an independent addition at the same location: retain the Activity presentation requirement and the new GitHub Integrations requirement, alongside Diagnostics ownership. Automatic reconciliation preserves Activity's private styles and exact draft/selector behavior while composing the new Agent Worker and Integrations presentation, owning instructions and contracts. Retain the previous bounded server-preferences integration wait repair.

Generated DeliDev client build and desktop typecheck passed. `pnpm exec vitest run src/activity-sidebar.test.tsx src/settings.test.tsx src/integrations.test.tsx src/agent-configuration.test.tsx --maxWorkers=1` passed all 76 tests in four files. These focused component fixtures verify the affected presentations and do not establish native CEF geometry or focus acceptance.

At the initial remote inspection, the PR remained open with merge conflicts, no unresolved Codex review threads and no failing reported check. Only Cloudflare Pages had reported a successful check for this head; absent CI/review evidence is not approval or complete validation. The five-minute maintenance heartbeat remains active.
