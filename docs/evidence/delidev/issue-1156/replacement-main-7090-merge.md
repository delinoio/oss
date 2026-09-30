# Replacement merge with main 7090de046

Date: 2026-09-30. PR #1217 head before repair: `9ffb39428c26696dd494efe5a25ae909f2a98f56`. Fetched main: `7090de046`, the verified Grok input-accounting change from PR #1211.

GitHub reported DIRTY. Merged `origin/main` without rebasing. The sole textual conflict was the final appended frontend ownership rule: retained both the Activity presentation rule and the incoming Usage/Grok accounting rule. The desktop contract merged both independent sections. Incoming runtime/schema/generated files were already mutually consistent on main and required no manual conflict resolution; the Activity implementation changes none of them.

The generated client build and package-local `pnpm typecheck` passed. `pnpm exec vitest run src/activity-sidebar.test.tsx src/sidebar.test.tsx src/grok-accounting.test.tsx src/usage.test.tsx --maxWorkers=2` passed all 45 tests in four files. Initial repair inventory had no unresolved actionable Codex threads or failing CI checks; pending checks are not counted as passed. Native/browser coverage remains limited as recorded in the other issue evidence files.
