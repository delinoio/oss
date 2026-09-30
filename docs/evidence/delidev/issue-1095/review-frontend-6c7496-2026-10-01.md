# Issue #1095: frontend validation after fork/restore reconciliation

The implementation source is `54403048beada6db90f1ff41ff508962ace487bb`,
incorporating main `6c749670727b30679e722821846bc8dc00f5ac32`. The complete
`apps/delidev/src` tree has no diff against that incoming main. This independent
record preserves the earlier frontend failures and qualifications.

## Executed checks

From `apps/delidev`, the required `pnpm test` completed with exit 1 after
527.90s: 30 failed / 70 passed files, 182 failed / 1,090 passed / 7 skipped
tests (1,279 total). Its API-client build and frontend typecheck passed; seven
settings integration suites failed their Go-build setup. Component failures
include bounded UI timeouts. The command stopped before its downstream fixture
and production build steps. This is not a complete-suite pass.

```sh
pnpm exec vitest run --maxWorkers 1 \
  src/App.test.tsx src/session-fork.test.tsx src/subscription-controller.test.tsx
```

This completed with exit 1 in 182.62s: 48 passed / 7 failed of 55 tests,
two passed files and one failed file. Fork and subscription-controller tests
passed. The seven App failures all exceeded the original 5,000ms test bound:

- Targeted entry disposal within an opening.
- Abandoning uncertain New Project save on reopening.
- Preserving session/draft through Settings navigation.
- Instructions disposal and targeted repository entry.
- Notification draft disposal without save.
- Wide header/Search focus handoff.
- New session draft/selected conversation through header destinations.

A further single-worker run selected those exact seven cases through `-t`
without widening their time limits. All seven passed, with 38 unrelated cases
skipped, in 32.76s. This isolated control does not erase either broader failure
or prove their whole-suite cause. The machine had other active Go/native
validation processes; no unrelated process was terminated.

The downstream steps were then run explicitly:

```sh
pnpm test:bundle-dry-run
pnpm test:desktop-launch
pnpm test:widget
pnpm build
```

All passed: 8 packaging tests, 16 launch/asset tests, widget fixtures and
the production build. Required source assets were already LFS-hydrated.
These are controlled packaging checks, not installed native/platform acceptance.

From `packages/delidev-api-client`, `pnpm typecheck` and `pnpm test` passed
with all 46 tests across five files (12.91s), after explicitly warming the
current Go CLI build. Root pinned protocol checks, six structural/allocation
tests and Go protocol/reflection checks also passed. The complete Go race run
is recorded separately. Generated distributions are removed before delivery.
