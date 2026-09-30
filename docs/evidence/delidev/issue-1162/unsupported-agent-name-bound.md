# Unsupported Agent name projection review repair

This repair addresses [Codex review thread PRRT_kwDORRAKg86ncGay](https://github.com/delinoio/oss/pull/1186#discussion_r4142237372) on PR #1186, after the merge recorded in [main-merge-126641a6.md](main-merge-126641a6.md). The finding is actionable: bounding an unknown-schema document to 1 MiB did not separately bound its projected name/alias before repeating it in a heading and three accessible labels.

The existing Go `domain.Agent.Validate` calls `Text` with a 256-byte name maximum. The display-only projection now checks code-unit length before allocating an encoding buffer, then applies that UTF-8 byte limit. A valid selected name/alias remains exact; an oversized value keeps Unnamed, disabled unsupported-schema actions and the complete resource ID. No truncation, editor/request change or extra query is introduced. Scoped instructions and the desktop contract record the bound.

Validated source SHA-256 on 2026-09-30:

- `apps/delidev/src/settings.tsx`: `df2457342bda55f7e480f36aee0eacf7349a2f1c9452139ab09c98d73bea3f80`.
- `apps/delidev/src/settings.test.tsx`: `cb0051eb764f8780711e1047ec41853011a117738613950ce0103c13ed12e064`.

## Validation

- `pnpm --dir apps/delidev typecheck` and focused `pnpm --dir apps/delidev exec vitest run src/settings.test.tsx --maxWorkers=1 --testTimeout=30000` passed all 43 Settings tests. Six new cases cover retained 256-byte ASCII and multi-byte names, rejection immediately above that bound, and 512 KiB name/alias values with bounded fallback labels, full IDs and disabled actions.
- `GOCACHE=/private/tmp/delidev-1162-go-cache pnpm --dir apps/delidev test` completed with exit 0: generated client build, typecheck, 88 Vitest files/1,071 tests, eight package/bundle fixtures, sixteen asset/launch fixtures, widget checks and production build. Vitest took 162.59 seconds.
- The complete run used temporary one-worker execution and 30-second test/hook/Testing Library budgets, as documented in earlier evidence. Assertions were unchanged, and configuration/setup were restored byte-for-byte before committing. This establishes the bounded serial invocation, not a successful default-budget run.
- `git diff --check` and `git lfs fsck` passed; generated repository-owned `dist` output was removed before staging.

This change adds source/fixture evidence only. The original browser observations and unperformed native CEF acceptance retain their qualifications; no new native or missing-platform acceptance is claimed.
