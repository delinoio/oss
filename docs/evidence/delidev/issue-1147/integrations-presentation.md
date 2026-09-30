# GitHub Integrations presentation evidence

## Scope and revision

- Issue: [#1147](https://github.com/delinoio/oss/issues/1147).
- Inspected base: `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`.
- Implementation: `cec4112425cdb54f9f6858645ce8b87bc01c9414`.
- Executed on 2026-09-30 on macOS arm64. This record covers the approved
  content presentation inside the existing Settings shell. No protocol,
  server operation, PAT storage, dependency, or bundled image changed.

## Component and package checks

Executed from `apps/delidev` after a frozen root install, hydration of the exact
desktop icon LFS object, and API-client generation:

- `pnpm exec vitest run src/integrations.test.tsx`: **22 passed**. Coverage
  includes successful first-page empty state, initial loading/read failure,
  cached refresh and stale results, opaque later/continuation pages, supported
  and unknown metadata, named row actions, first-entry focus, immutable rename
  metadata, valid token input, pending replacement identity, uncertain save,
  separate storage/identity observations, exact identity timestamps, retained
  Classic guidance/status, and disclosure toggles without form reads/opening.
- `pnpm exec vitest run src/integrations.test.tsx src/github-opening.test.tsx
  src/settings.test.tsx src/App.test.tsx --maxWorkers=2`: **86 passed** before
  the final two Integrations regressions were added. The final separate
  Integrations run above includes those regressions.
- `pnpm exec vitest run src/settings-preferences.integration.test.tsx
  src/settings-devices.integration.test.tsx src/tray-presentation.test.tsx
  --maxWorkers=1`: **3 passed** in the earlier independent recheck.
- Final combined run of the seven files above with `--maxWorkers=1`:
  **90 passed, 1 failed**. The existing real-server preferences fixture timed
  out waiting for its button. The other six files, including Settings opening
  disposal, native-opener fixtures, and all 22 Integrations cases, passed.
- Required `pnpm test` was executed. Its typecheck and generated-client build
  passed; the full unit suite did not pass. Both bounded attempts
  (`VITEST_MAX_WORKERS=2 pnpm test` and `VITEST_MAX_WORKERS=1 pnpm test`) finished
  with **972 passed, 5 failed** out of 977. The serial failures were existing
  App notification/import disposal, Settings account disposal, tray presentation,
  and real-server device/preferences cases. They hit existing test deadlines or
  asynchronous button waits; no deadline or assertion was weakened. The
  different failure sets and independent passes are evidence of intermittent
  behavior, not proof that a full suite passes. All Integrations tests passed
  in these runs. An unrestricted final attempt also failed (928/977 passed).
- The unit failure prevented the later `pnpm test` stages from running, so
  `pnpm test:bundle-dry-run && pnpm test:desktop-launch && pnpm test:widget &&
  pnpm build` was executed separately: **passed**, including 8 packaging
  fixtures, 16 desktop-launch/asset fixtures, widget fixtures, and production
  frontend build.
- `git lfs fsck`: **passed**. Generated repository-owned `dist` directories
  are removed after validation and are not part of this change.

## Actual native observation

`CARGO_TARGET_DIR=<existing local Cargo cache> pnpm dev:desktop -- --data-dir
<private temporary smoke state>` built and launched the ad-hoc macOS arm64
CEF bundle through the repository launcher. The pinned CEF distribution was
150.0.10. A separate disposable loopback Go server and explicit paired client
provided real profile persistence; no user server, PAT, or GitHub account was
used. The occupied default local-server port failed closed before the separate
saved connection was selected.

Observed the actual native window at a wide 1728×1052 webview and a resized
960×640 webview (960×672 decorated window, 1920×1344 screenshot raster on the
2× display):

- All 16 Settings categories and the AI Subscription label remained present.
  The Integrations description, GitHub panel, single empty-state creation
  action, static three-step explanation and selected-server storage caveat
  matched the contract; empty first-page paging was hidden.
- Keyboard creation focused Profile name, accepted name/owner metadata,
  and returned to the profile list after Save. No token form opened.
- Manage displayed separate No token connected and Not verified facts,
  full expanded fine-grained guidance, a blank protected token field with the
  placeholder, disabled token submission/validation, and separate outlined
  deletion control. Disclosure keyboard toggling and the original Keep profile
  confirmation path were exercised without a token or external navigation.
- At 960×640, row actions wrapped below metadata, text and facts stayed inside
  the content width, and Manage guidance scrolled vertically. Escape closed
  Settings and restored its opener focus; a fresh opening discarded Manage
  state while the saved profile persisted.

These are actual macOS visual/keyboard observations, not jsdom native evidence.
They do not establish PAT storage/validation, GitHub permissions, Windows/X11
acceptance, or long-running renderer stability. A later screenshot capture
after the browser check returned a blank native surface; the native run also
logged an ad-hoc signature-validation failure. No valid final screenshot was
retained from that capture, and this record does not claim the late capture
passed. The owned native process and temporary server were stopped, and the
paired client was removed through its exact revision-bound removal command.

## Browser zoom observation and limits

A temporary production build imported the actual Settings, Integrations,
documents and stylesheet modules, used an in-memory Connect transport, and
served only on loopback with a strict self-only CSP. It was not repository
product code, a bundled fixture image, or an account/native acceptance test.

Chrome's viewport override was set to 960×640. Chrome's visible zoom control
confirmed **200%**. The empty-state copy wrapped, keyboard profile creation
returned to the list, and Manage retained the blank disabled protected-input
state and informational disclosure. The zoomed Connect section and token field
fit horizontally and used vertical scrolling. DOM geometry measurement and a
final browser screenshot could not complete because browser debugger commands
timed out; no numerical DOM-overflow assertion is claimed. The zoom was reset
to 100%, the viewport override was reset, and the temporary preview was stopped.

The native CEF menu/shortcuts did not expose a working zoom control, so 200%
native zoom remains unverified. Actual Windows and Ubuntu X11 sessions were
unavailable on this macOS host. No fixture result is presented as acceptance
on those platforms.
