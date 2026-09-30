# Issue #1145 main merge maintenance, 2026-09-30

Inspected the merge working tree from PR #1183 head
`eb46c6f4eea72b35f98a7ad692613b06dc885dc1` and fetched `origin/main`
`126641a6dcf274420c8c5800a6e88079f0e19990`.

## Resolution

The only conflict was in `apps/delidev/src/AGENTS.md`. The resolution retains
all incoming instructions, including the issue #1155 Pull requests sidebar scope,
and the complete issue #1145 account rule: immediate native provider actions,
independent bounded inventory/cursor, all four capability gates, exact provider
row identity/event-key consumption, focus restoration and transient-secret guards.
An exact comparison confirmed the resolved file equals the incoming instructions
with only the older account rule replaced by this PR's complete account rule.

The incoming Pull requests sidebar, GitHub Integrations, Agent Worker forms and
PR Activity source/schema changes merged without source conflicts. This pass
adds no provider-picker behavior change. The issue #1135 AI API Keys terminology,
Settings opening lifecycle and standalone presentation scopes remain preserved.

## Validation

Commands ran from `apps/delidev` with the existing private temporary
`GOCACHE=/tmp/issue-1145-go-cache` for Go-backed tests.

- Required `pnpm test`: API-client generation build and frontend typecheck passed.
  The full Vitest run passed **1,054 of 1,064 tests**, with ten failures across
  App, Agent configuration, Pull requests, Settings lifetime, Server preferences
  integration, Settings and tray presentation. Eight exceeded the unchanged
  five-second deadline; the other two were async UI lookups. The command stopped
  before packaging/build scripts. No full-suite pass is claimed.
- `pnpm exec vitest run src/account-settings.test.tsx src/settings.test.tsx
  src/settings-configuration.integration.test.tsx src/pull-requests.test.tsx
  src/integrations.test.tsx src/agent-configuration.test.tsx --maxWorkers 1
  --no-file-parallelism --testTimeout 30000`: **all 110 tests passed across six
  files in one run**, including the real Go configuration fixture. This explicitly
  serial focused run covers the provider picker and adjacent incoming presentation
  changes. Repository timeout and concurrency settings were unchanged.
- `pnpm test:bundle-dry-run`: all eight passed.
- `pnpm test:desktop-launch`: all sixteen passed.
- `pnpm test:widget`: passed.
- `pnpm build`: passed.
- `git lfs fsck` and `git diff --check`: passed.

Generated app and API-client `dist` directories were removed after verification.
These results are component, temporary-server and build observations. They do
not establish native CEF acceptance, supported-platform packaging acceptance or
real hosted-provider authentication. Earlier browser evidence and its limits
remain in [the picker record](provider-picker.md).
