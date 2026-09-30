# Pull requests sidebar presentation (#1155)

## Source and implementation

The issue branch starts at freshly fetched main
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` on 2026-09-30. The tested working
tree contains the approved PR-only presentation changes in this commit: active
root scope, repository glyph/name/full UUID, page-scoped folder empty block,
divided Query options, visible explicit-load guidance, and compact expanding
inline footer controls. The existing rail/pane/drawer, controllers, query owners,
connection-memory selections, pending mutations and Settings opening lifecycle
remain authoritative. No Rust, Go, protocol, dependency, authorization or native
window behavior changed. The desktop contract and source-scoped instructions
record the presentation; stale permanently-mounted Settings wording is reconciled
with its existing close/disposal contract.

## Component and package verification

Root `pnpm install --frozen-lockfile` passed, including linked-worktree Lefthook
installation. `pnpm --filter @delinoio/delidev-api-client build` prepared the
required generated client before frontend checks. This frontend-only build does
not consume repository LFS assets; native source-icon hydration was not needed.
Asset-preparation tests use their own temporary Git/LFS fixtures.

The new generated-transport `pull-requests.test.tsx` suite passed all 11 cases
with `pnpm exec vitest run src/pull-requests.test.tsx --maxWorkers=1`. It proves
empty/loading/error distinctions, exact default Load payload, no GitHub request
on selection/filter editing/catalog paging, one necessary off-page resource read,
cached First-page reuse, retained applied results while filters are drafts,
inactive-result disposal, same-identity reconnect/control retention, repository
replacement defaults, unsupported/missing configuration guidance and the existing
120-character/plain-search boundary.

The first focused attempt contained four new fixture assertion mistakes (exact
heading text, cached First-page behavior and the unsupported-schema fallback
name); those were corrected without changing product behavior. Six unchanged
App tests also exceeded their existing deadlines. The ordinary full `pnpm test`
attempt passed client generation/type checking and 969/971 tests in 83/85 files;
the remaining App and tray cases timed out. A two-file single-worker rerun also
failed under concurrent host work (31/38 passed), including timing-related
asynchronous element waits. No deadline was raised and no unchanged test or
product behavior was altered to accommodate these attempts.

Separate package stages passed: all 8 bundle-verifier cases, all 16 asset/launcher
cases, native Swift widget fixtures, and `pnpm build`. The complete pipeline rerun
and cleanup outcome are recorded below after completion.

## Rendered browser verification

A temporary, unshipped Rsbuild fixture rendered the real App/sidebar components,
generated Connect router transport, production stylesheet, LocalServerControls
and LocalRegistrationRecovery in the installed Chrome browser on macOS arm64.
It used illustrative bounded repository data and a fixture-only revoked native
registration response; it did not contact GitHub or a user's server.

Computed layout checks passed at 1440x900, 1101x900, 1100x900, 960x640,
760x900 and 759x900 CSS pixels. The persistent pane measures 288px above 1100,
256px through 1100, with the original 288px modal drawer below 760; the rail
remains 52px. Insets are 16px, titles/body/UUID metadata are 17/13/12px,
selected fill is rgb(231,239,255), controls/Load are at least 40px high and
corners are 8px. A long name and the complete exact UUID wrap without horizontal
overflow. Middle and footer retain independent auto overflow and the footer's
35% maximum.

Expanded Local server stop confirmation and long permission guidance remained
horizontally unclipped at every viewport; action rows measured 37.5px with a
36px minimum. The footer scrolled vertically and the confirmation stayed
reachable. The actual registration modal's computed width, padding, corners,
font, paragraph font and button geometry were identical under PR and Sessions:
700px width, 24px padding, 16px corners, 16px inherited font, its existing 11px
paragraphs and 40px-minimum buttons with 8px/13px padding. PR footer overrides
exclude nested footer dialogs while still applying inside the outer drawer.

The real browser drawer closed on Escape and restored its opener. A live
759-to-760px resize retained the Closed filter. Programmatic background focus
was rejected; 25 Tab steps never reached a background app control, with one
transition to Chrome browser chrome. The focused drawer control retained its
3px solid outline. Native desktop focus acceptance remains separate.
For 200% zoom-equivalent reflow, Chrome device metrics used a 720x450 CSS
viewport with scale factor 2, corresponding to 1440x900 physical pixels. Empty
and selected states had no horizontal overflow, and scrolled actions remained
reachable. This is CSS/browser reflow evidence, not a native desktop zoom test.
Screenshots of the empty wide and selected wide/minimum states were inspected.

## Limits

No updated native CEF desktop binary was launched for this task. macOS native
window/minimum-size and native 200% zoom acceptance, Windows and Ubuntu X11
runtime/visual/focus checks remain unperformed. Browser dialogs and jsdom stubs
do not establish those platform results. This record does not claim live
GitHub/server, account recovery, signed packaging, release or deployment
acceptance. Generated design pixels are supporting evidence only and were not
added to the product. Temporary fixtures and repository-owned generated dist
output are removed before publication.
