# Schedules context presentation validation for issue #1153

## Revision and scope

- Recorded on 2026-09-30 for issue [#1153](https://github.com/delinoio/oss/issues/1153).
- Tested implementation: `f611afb7a45577e989bf8d0836b1ecc869efe895`, based on freshly fetched main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`.
- The change adopts the approved Schedules-only hierarchy, horizontal enum filters,
  `All projects` empty choice, whole-row selection with complete UTC metadata,
  distinct successful empty states, and an initially collapsed retained-history
  lookup. Its connection-memory draft and disclosure survive navigation and
  same-identity reconnects. Focus follows only explicit expansion.
- Shared selector/outlet hooks preserve their defaults. The existing shared rail,
  header, footer, main workflows, protected replacements, mutation retry ownership,
  RPCs, schemas, scheduling defaults and persisted configuration remain unchanged.
  No dependency, font, image asset, telemetry or history-input logging was added.
- This is a separate issue record under the current evidence ownership policy.
  The historical `docs/cmds-delidev-evidence.md` ledger is preserved.

## Executed component and package checks

1. `pnpm install --frozen-lockfile` at the worktree root completed, including the
   linked-worktree hook installation.
2. `pnpm --filter delidev-desktop exec vitest run src/schedules-sidebar.test.tsx src/schedules.test.tsx`
   passed: 2 files, 23 tests.
3. `pnpm test` from `apps/delidev` completed successfully: generated API client
   build, TypeScript checking, 85 Vitest files / 976 tests, 8 packaging dry-run
   tests, 16 desktop launcher/asset tests, Swift widget fixtures and production
   Rsbuild build.

The full successful run used temporary local test-runner settings:
`maxWorkers: 1`, `testTimeout: 60000`, and Testing Library
`asyncUtilTimeout: 15000`. Both configuration files were restored afterward and
have no diff. The initial run with the repository defaults failed seven existing
Settings/App fixtures on their timing limits; a two-worker retry also encountered
timing failures and was stopped. These runs do not establish a default-timing
pass. The successful run exercised all tests rather than filtering failures.

The added regression coverage checks exact row/UTC state and selection; bounded,
independent selector/list pages and immediate filters; Refresh/First/Next;
loading, first/later empty pages and typed permission/authentication/connection
errors; cached rows after same-scope refresh failure and clearing across scopes;
mounted hidden history draft, explicit expansion focus and trimmed submission;
portal placement without refocus; editor, confirmation, in-flight and uncertain
workflow locks with exact Run now retry identity/bytes; unchanged ResourceChoice
defaults; and App navigation/reconnect retention versus identity reset.

## Browser layout and keyboard checks

A temporary Rsbuild entry rendered the real App shell, Schedules controller and
production stylesheet with a generated Connect in-memory router. It supplied
non-secret populated, empty and Unavailable-error responses, including a long
unbroken title and `2026-10-02T01:00:00.123456789Z`. It did not invoke a real
account, Worker, provider or production mutation. Both temporary source/config
files were removed after the checks.

Headless Chrome `154.0.8037.59` passed 15 viewport/state combinations:

| Effective CSS viewport | Device scale factor | States |
| --- | --- | --- |
| 1440 × 900 | 1 | Populated, empty, error |
| 1100 × 768 | 1 | Populated, empty, error |
| 960 × 640 | 1 | Populated, empty, error |
| 720 × 640 | 1 | Populated, empty, error |
| 720 × 450 | 2 | Populated, empty, error |

Each combination exercised expanded history with a 36-character ID. Assertions
checked document/pane/row/segment horizontal bounds, the shared 52px rail,
independent content scroll bounds above the footer, at least 36px segment height,
selected-row semantics and the complete fractional UTC instant. Long rows wrapped
within their bounds; the single-line ID field retained its normal internal text
scrolling. Screenshots were inspected locally. This inspection exposed an
outward focus outline clipped by the scroll pane; the final scoped inset focus
outline was rebuilt and the matrix rerun successfully.

Keyboard checks covered Enter and Space disclosure activation, input focus on
expansion, exact draft retention after collapse/re-expansion, and Tab to the
existing submit action. Compact drawer checks cycled Tab without entering
background application controls, then verified Escape closure and opener focus
restoration. Browser chrome/body focus at the native-dialog boundary was allowed
by the fixture assertion; it is not evidence of an added application focus trap.

The 720 × 450 / scale-factor-2 case represents the effective CSS geometry of a
1440 × 900 surface at 200% magnification. It is an effective-width/reflow fixture,
not an actual Chrome or native CEF 200% zoom setting. Actual 200% zoom acceptance
remains unverified.

## macOS native smoke

- Platform: macOS 26.6.2, arm64.
- `pnpm prepare:assets` hydrated the required tracked native icon from Git LFS;
  `git lfs ls-files --include='apps/delidev/**'` reported hydrated content.
- The documented `pnpm dev:desktop --data-dir <private temporary directory>`
  workflow completed `build:native`: frontend/client builds, Go sidecar, both
  Swift widget extensions with ad hoc signatures, and pinned CEF Rust host.
- Native compilation used an isolated copy-on-write target cache. Disappearing
  generated artifacts during the initial cache copy were rebuilt; relocated
  generated CMake caches were removed before rebuilding.
- The first normal launch hit the existing CEF cache singleton and returned a
  sidecar startup failure. The smoke retry reused the existing exported desktop
  argument/environment builders and pinned CEF launch path with the test-only
  bundle identifier `io.delino.delidev.smoke1153`. This isolated its cache from
  another running app. No repository launcher, bundle configuration, dependency,
  entitlement or production identifier was changed for the retry.
- The isolated ad hoc app launched successfully. A separate private loopback
  sidecar was paired through the existing CLI into the disposable app data
  directory. The native saved connection opened Schedules and fetched its actual
  empty schedule list through Connect.
- The native empty pane displayed the horizontal filters, `All projects`,
  `Saved schedules`, visible Refresh text, first-page empty state, disabled page
  controls and initially collapsed history with the shared footer separate.
- Clicking the disclosure focused the ID input. A non-secret UUID-v7 draft
  survived collapse and Space re-expansion. Activity → Schedules navigation kept
  the expanded lookup and exact ID while focus stayed on the Schedules rail
  control. Tab from the ID reached the existing submit action. Disclosure and
  navigation did not submit history or replace the main workflow.
- The inspected native screenshot was 3440 × 2168 physical pixels at Retina
  scale 2, approximately 1720 × 1052 content CSS pixels below the title bar, at
  100% app zoom. No clipping or footer overlap was observed in that empty state.
  The required smaller viewports were exercised by the browser matrix above,
  not by native window resizing.

This smoke establishes one macOS arm64 ad hoc CEF launch and the stated empty-list
interaction. It does not establish Windows/X11 behavior, actual native 200% zoom,
all native viewport/state combinations, signed release packaging, provider/Worker
execution, schedule creation/mutation, or broader desktop/account acceptance.
Native signing/password-store warnings were present; they do not count as release
or secure-storage acceptance.

## Cleanup and limits

The task's native app exited, and its disposable server and browser-fixture server
were stopped. Process inspection found no remaining task-owned smoke processes.
The temporary fixture entry/config and every repository-owned generated `dist`
in this worktree were removed; the remaining-dist scan excluded dependency
`node_modules` and ignored native `target` caches. The temporary test timing
changes were restored. Test credentials and task-specific app/cache state were
removed after shutdown. The primary checkout's pre-existing edits and unrelated
running apps/servers were preserved.

No Rust source was modified. Frontend/package and native build results above are
executed evidence. The explicit platform/zoom gaps remain unavailable; they must
not be converted into a complete native acceptance claim.
