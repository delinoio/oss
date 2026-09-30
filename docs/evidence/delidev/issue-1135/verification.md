# AI API Keys terminology verification

## Source and scope

Recorded on 2026-09-30 for [issue #1135](https://github.com/delinoio/oss/issues/1135).
The implementation revision is `31cc2222d5f46e2d6528671ef4c1dc4a42dd0ef0`, based on
main revision `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`.

The existing `api-accounts` category now presents AI API Keys in the sidebar,
compact selector and heading. API-only list, wizard, preferences, connection,
deletion, provider counts/actions, Models help and accessible names use entry
terminology. The masked secret remains API key; keyless local connections keep
an explicit endpoint action and endpoint-only failure guidance. Subscription,
mixed Agent/project/routing selections, aliases and server diagnostics retain
account terminology. Main already presents the subscription category as
AI Subscription under issue #1134; this change preserves that current label.

Code inspection confirms unchanged Account resources, RPC/CLI names, stored
fields, IDs, revisions, capability gates, provider/type filters, cursors, counts,
credential ownership, mutation payloads/retries, navigation locks and late-response
guards. No backend/native source, schema, migration, feature flag or logging was
changed. The ordinary desktop frontend production build owns this presentation.

## Automated validation

DeliDev assets were hydrated with `git lfs pull --include='apps/delidev/**'`.
`pnpm install --frozen-lockfile` at the worktree root installed dependencies and
prepared the linked-worktree hooks. `git lfs fsck` passed.

The updated existing tests retain exact create/connect request and revision
checks, secret masking/clearing, uncertain retries, provider restrictions,
subscription metadata, pagination and late-response/navigation guards. Added
regressions cover API/subscription accessible names and empty states, initial
loading and failed refreshes retaining entries, keyless failure without key
reentry, API-only preference/deletion copy, preserved aliases/server diagnostics,
and independent connection/validation/disconnection actions. The existing real
Go integration fixture explicitly validates a temporary keyless loopback provider
and continues to select the resulting Account through mixed Agent selectors.

The first focused run exposed old label expectations, which were corrected.
A later focused run completed 40 tests and hit two existing five-second Settings
timeouts. The default full `pnpm test` run also reported five-second timeouts in
multiple unrelated suites and was canceled; that run is not a passing result.
Validation was restarted with the installed Vitest version's supported
`VITEST_MAX_WORKERS=1` setting, preserving the package command and test timeouts.

The first single-worker run passed all 84 Vitest files and 965 tests but returned
nonzero for one unhandled late React Query callback (`window is not defined`)
after `App.test.tsx` teardown. That run is not an aggregate pass. App source/tests
and global test setup are unchanged from the inspected main revision.

`pnpm exec vitest run src/App.test.tsx src/account-settings.test.tsx src/settings.test.tsx src/settings-configuration.integration.test.tsx --maxWorkers=1 --testTimeout=15000`
then passed all four files and 80 tests without an unhandled error, including the
real Go keyless provider integration. The explicit per-test timeout is recorded;
no repository test-runner settings were changed.

`pnpm test:bundle-dry-run`, `pnpm test:desktop-launch`, `pnpm test:widget` and
`pnpm build` passed independently: eight bundle tests, sixteen launch/asset tests,
widget fixtures and the ordinary production frontend bundle.

The repeated complete `VITEST_MAX_WORKERS=1 pnpm test` command exited zero:
all 84 Vitest files and 965 tests passed without an unhandled error
(843.07 seconds), followed by all eight bundle tests, all sixteen launch/asset
tests, widget fixtures and the ordinary production build. Generated
repository-owned `dist` output was removed after validation.

## Layout and focus inspection

A temporary loopback Rsbuild fixture rendered the actual Settings components,
styles and generated Connect router transport without a native host, live
credentials or provider network requests. The fixture and browser were removed
following inspection.

Browser device metrics used 2 physical pixels per CSS pixel, equivalent to 200%
zoom: the desktop layout used 960 × 600 CSS pixels in a 1920 × 1200 capture, and
the compact layout used 640 × 600 CSS pixels in a 1280 × 1200 capture. Both had
`document.documentElement.scrollWidth <= innerWidth`. Labels and descriptive copy
wrapped inside the viewport, the compact selector retained AI API Keys and the
same category value, and keyboard focus had a visible three-pixel outline.
The renamed Provider → Details wizard retained its navigation lock, Entry name
and masked API key controls, with no horizontal overflow.

- [Desktop at 200% equivalent scale](desktop-200.png)
- [Compact selector and focused Add action at 200% equivalent scale](compact-200.png)

This is frontend browser layout evidence. Native CEF zoom, OS keyboard behavior,
packaged desktop launches, real hosted credentials and real local model servers
were not exercised. It does not establish the broader issue #964 native/account
acceptance requirements.
