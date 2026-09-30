# Home-only sidebar actions (issue #1149)

Date: 2026-09-30. Inspected base: `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`
(`origin/main`, freshly fetched before implementation). Implementation revision: `5ca741e9c6747970d7ee2b6069c45a83a34f3633`
on `kdy1/delidev-home-actions-1149`.

## Implemented boundary

The complete Inbox/Search header group renders only on Sessions and NewSession,
independent of read state. The shell consumes a header-origin destination in its
synchronous layout effect after child drawer closing. Wide focus moves to main;
compact focus moves to the persistent destination opener. Existing Search
first-entry autofocus remains authoritative. Replacement navigation and Settings
clear pending intent; tray and notification navigation retain their focus rules.
No CSS, business RPC, query/storage model, dependency or native-host code changes.

Generated-router component regressions cover all eight surfaces and loading,
empty, PermissionDenied, Unavailable and cached-refresh states; header action
order/names/decorative SVGs and group absence; unchanged reads when hiding;
wide/compact focus, first/repeated Search entry, superseding navigation, Settings,
resize and refetch; retained conversation/composer/NewSession drafts, Search and
Inbox filter drafts/applied filters, Search pagination, independent sidebar page
scopes, expanded groups and per-surface scroll. Spies assert no enqueue/control,
create, Inbox read-state/response, configuration or GitHub mutations from navigation.
Existing reconnect and identity-reset tests remain in the package suite.

## Browser observations

A temporary production Rsbuild entry mounted the real App against synthetic
Connect router fixtures from App.test.tsx; no real account, credential, server,
provider, GitHub write or Worker action was used. Observed through the Codex
in-app Chromium browser on macOS arm64:

- At 960×640, Tab reached Inbox from Settings and Search from Inbox. Inbox Enter
  activation focused main; Search Space activation focused its query on first
  entry. On repeated entry main kept focus, with the query and Archive draft intact.
  Keyboard Inbox focus had the existing visible 3px blue outline.
- At 759×640, header activation closed modal mode and focused the renamed Inbox
  or Search opener. Fresh compact Search waited for explicit drawer opening before
  focusing its query. Escape and Close returned to the current opener. While the
  modal was open the accessibility tree contained only its content; twelve Tab
  steps visited modal controls or browser document focus, with no background
  control receiving focus.
- Home and non-home DeliDev title rectangles were identical at 759, 760, 960,
  1,100 and 1,101px: x=64, y=18.203125, width=62.0703125,
  height=21.59375 CSS px. Header height stayed 34px and rail width 52px.
  Pane width was 256px at 760/960/1,100, 288px at 1,101, and the existing 288px
  modal drawer at 759. Non-home had zero header buttons and no action group.
  Wide Pull requests had no horizontal page overflow.
- A 480×320 CSS viewport (the layout size of 960×640 at 200% zoom) retained visible
  Close, both home targets and a scrollable drawer with no horizontal overflow.
  The browser ignored five Command-plus presses: innerWidth/height and DPR did
  not change. This is equivalent-viewport reflow evidence, not actual 200% zoom.

The temporary entry/config and browser tab are removed after verification.
The final Pull requests screenshot is an untracked temporary artifact.

## Verification

- `pnpm prepare:assets` restored the exact source icon from the shared local LFS
  cache. `git lfs fsck` passed. Type checking and API-client generation passed.
- Required `pnpm test` passed with 84 Vitest files / 973 tests, 8 package-verifier
  fixtures, 16 launcher/asset fixtures, native Swift widget fixtures and the
  ordinary production frontend build. No Rust source was changed.
- The first default local run failed 44 tests under severe host load; a two-worker
  run reduced that to seven existing Settings/tray timing failures. An isolated
  six-file run with one worker and a 15-second test budget passed App, Settings,
  tray and configuration tests; two Settings integration reads still exceeded
  the default one-second element wait. The observed host load averages were
  480.71 / 464.40 / 432.02.
- The final complete `pnpm test` run used temporary local overrides only:
  `maxWorkers: 2`, `testTimeout: 15000` and Testing Library `asyncUtilTimeout: 5000`.
  No assertions were changed or skipped. Both runner/setup files were restored
  byte-for-byte afterward and are absent from the implementation diff. This
  establishes suite success with those local timing budgets, not success of the
  earlier default-budget runs.
- Generated app/client `dist` directories and temporary browser source/config
  were removed. `git diff --check` passed. Packaged/native limits remain below.

## Remaining acceptance limits

No packaged Tauri/CEF process was launched for this revision. Native window
geometry, tray/notification activation, OS-specific runtime behavior, actual 200%
zoom and reduced-motion interaction were not exercised. CSS/native-host source
was unchanged; that inspection does not establish macOS/Windows/Ubuntu native
acceptance. The browser fixture and jsdom dialog stubs do not establish packaged-host
focus/inertness or native window sizing. No release/publication was performed.

The current main structure contract requires new evidence under issue-owned files
and freezes docs/cmds-delidev-evidence.md; this independent record preserves that
newer ownership rule while providing issue #1149's requested verification evidence.
