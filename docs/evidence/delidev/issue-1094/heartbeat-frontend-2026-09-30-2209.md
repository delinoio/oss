# Desktop validation after current PR #1225 repairs

Runtime source: `f43bcf551` (the four repair commits). Later commits during this validation add independent evidence only. No frontend/native/protocol source changed during these checks.

Required assets and ignored API-client/embed output were explicitly prepared first. `node apps/delidev/scripts/prepare-assets.mjs` reported the exact DeliDev LFS icon ready; no pointer-only asset was compiled. The complete required command ran from `apps/delidev` with unchanged default test deadlines:

```sh
GOMAXPROCS=4 pnpm test
```

API-client build and frontend typechecking passed. Vitest then failed: 79 of 103 files passed, 24 files failed, and 1,205 tests passed, 111 failed and seven skipped (1,323 total; 242.97s). Seven suite failures occurred during isolated temporary `go build ./cmds/delidev-cli` fixture setup: Claude, configuration, devices, GitHub, preferences, subscriptions and workspace Settings integration. The remaining failures include broader App/Settings/session/schedule/tray/notification presentation expectations and default deadline failures. These results do not prove those failures are pre-existing, unrelated or environmental. No timing assertion, deadline, ownership assertion or test was removed to obtain a pass.

The new child-page and adjacent hierarchy/streaming selection separately passed all three files and 43 tests at default deadlines (14.55s), and fresh typechecking passed. The complete run reported no failures in those three files. Exact counter/native source/retained omission and malformed complete-page evidence is described in [desktop-page validation](desktop-child-page-2026-09-30.md).

The default command stops after the Vitest failure, so its later stages were explicitly executed from the same frontend directory:

```sh
pnpm test:bundle-dry-run
pnpm test:desktop-launch
pnpm test:widget
pnpm build
```

All four commands passed: eight bundle fixture checks, 16 launcher/asset fixture checks, widget exact-value/currency/isolation/staleness/closure/masking/corruption/private-storage checks and the production Rsbuild build. Separately passing these stages does not turn the default `pnpm test` into a pass. These component, script and build checks do not establish native packaging, actual accounts, supported-platform runtime cleanup, signing or release acceptance.

Generated repository-owned `dist` remains ignored while backend compilation/checks need its embeds. It is removed after final validation and normal commit hooks; the final maintenance record confirms cleanup. Historical evidence remains unchanged.
