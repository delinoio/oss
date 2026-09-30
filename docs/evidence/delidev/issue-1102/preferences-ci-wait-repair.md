# Real-server preferences CI wait repair

## Source and failure — 2026-09-30

Repair base: `3fdd55a3`, PR #1166. GitHub Actions run `36681473848`, job
`109778030108` failed on head `16b70e5efdd2edb79918d785cdfdd9fe1892016f`.
Protocol generation/freshness and API-client checks passed before the job reached
the DeliDev frontend suite. That suite passed 959 tests and failed
`settings-preferences.integration.test.tsx` at the first post-save
`Edit Server preferences` lookup.

The installed Testing Library default asynchronous DOM wait is 1,000 ms. This
test uses a real Go server, durable save and subsequent list-query invalidation.
Both post-save lookups now use a bounded 5,000 ms wait. Exact singleton identity,
Go defaults, edited values and revision increments remain asserted. This changes
the integration-test wait only; no product save or retry behavior changes.

## Executed focused validation

Passed from `apps/delidev`, with task-private temporary Go module/build caches:
`GOFLAGS=-p=1 GOMAXPROCS=2 pnpm exec vitest run src/settings-preferences.integration.test.tsx`.

One test passed; total duration was 6.59 seconds. `git diff --check` passed.
The complete required frontend command is validated separately. These fixtures
use temporary local server state and establish no installed-desktop or release
acceptance.
