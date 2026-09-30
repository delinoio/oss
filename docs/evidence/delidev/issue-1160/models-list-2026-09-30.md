# Models Settings list redesign — issue #1160

## Source and implemented boundary

The issue was read from GitHub on 2026-09-30. Implementation starts from freshly
fetched main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` on branch
`kdy1/delidev-models-1160`. The owner-approved text specification is authoritative;
its ImageGen previews are supporting design references, not shipped UI evidence.

The Models list now has a single 1040px-bounded column, title-aligned New Model
action, 420px labeled search, neutral successful-empty panel, provider-grouped
semantic model rows with every existing metadata field and both visible actions,
and the existing policy footnote below results/pagination. Styles are scoped to
the list; Settings shell geometry, other categories and editors stay as before.

Only the Models query/cursor pair moves into the current Settings opening. Search
changes reset the cursor in one update. List/editor/pricing/category and
same-identity reconnect transitions preserve that pair within the opening;
#1138 disposal still drops it, pending client waits and exact retry presentation
on Close/Escape/navigation away. Existing mutation locks and original uncertain
write identities remain intact. No API/schema/storage/backend/native change is
included, and no automatic write, discovery, total count or page traversal is
added.

Inventory and model reads distinguish loading, capability/authority failures,
each successful empty scope, same-scope refresh and failed-refresh cached data.
A transient read's deliberate Retry uses only its own current refetch path, is
disabled while fetching, and cannot write or make an ineligible workflow eligible.
Known-zero account guidance requires complete nonempty enabled inventory and all
counts available. Manual model creation remains capability-gated and independent
of connected accounts.

## Browser evidence

A temporary static Rsbuild artifact reused the actual Settings component,
production stylesheet, generated Connect router and illustrative OpenAI/two-model
fixtures. No real account, credentials, inference or user server state was used.
Port 46311 was already owned by a static server; its process and original files
were preserved. The preview was served from a separate temporary subdirectory.
A failed attempt to start the normal fixed-port development preview stopped on
that conflict instead of remapping.

The Codex in-app browser inspected both empty and populated states at 1840×1196
CSS pixels. Measured column/search/title/action values were 1040px, 420px, 28px
and 40px. The empty first page had one heading/action, the exact optional-account
copy and no pagination. Populated rows retained NEW/Reviewed, Visible/Hidden,
native IDs, alias/None, configured harnesses/None, both actions and continuation.
No list element had an inline style.

At 960×640 and 760×640, the sidebar remained visible and row actions stacked;
the measured list widths were 672px and 472px. At 759×640, the sidebar was hidden,
the existing grouped selector appeared and the list width was 727px. Search stayed
420px whenever space allowed; all measured buttons/inputs were at least 40px high.
Long provider/model names, native IDs and aliases wrapped without horizontal
pane overflow or clipped text at 1840×1196, 960×640, 920×598 and 380×320. Content
remained vertically scrollable. The latter two checks cover halved CSS-viewport
reflow corresponding to 200% zoom; actual browser zoom was not exercised.

The exact production CSP from Tauri configuration was applied to the static
artifact through a temporary meta tag. Reloading and navigating the fixture
reported no browser warning/error, including no style/script CSP violations.
Tab from search reached Edit model with a solid visible focus outline. Space
opened the existing exact-resource editor; Escape closed the native HTML dialog
and restored the fixture opener. Forward/reverse boundary traversal passed
through document focus and then Close/Load more within the dialog, without
focusing a background control. The temporary viewport override was reset.
Screenshots are temporary local review artifacts, not tracked product assets.

## Automated verification

Required generated client input was built, root `pnpm install --frozen-lockfile`
installed the existing worktree dependencies/hooks, and `pnpm prepare:assets`
confirmed the hydrated source icon through its local-cache path. `git lfs fsck`
passed. No lockfile, dependency, imported-license or tracked-asset change resulted.

`pnpm test` from `apps/delidev` passed the complete pipeline: client build,
TypeScript checking, all 85 Vitest files / 982 tests (including 22 new Models
cases), 8 package dry-run fixtures, 16 launcher/asset fixtures, native Swift widget
fixtures and the production Rsbuild frontend build. The new cases cover exact
read ownership, all empty/capability/count branches, retained metadata/actions,
same-scope cached refresh failures, busy Retry, atomic query/cursor reset,
inactive reads, within-opening transitions, uncertain writes and closing/late
result disposal under React Strict Mode.

The committed default timing run did not pass on this heavily loaded host:
46 of 979 tests hit failures dominated by 5-second timeouts. After the final
fixture corrections/additions, worker-bounded reruns reported 2 of 982 failures
(two workers) and 8 of 982 failures (one worker); the latter failures were
existing App/Settings/tray timeouts and a Worker workflow's asynchronous wait.
The successful final invocation used a temporary local `maxWorkers: 2`, default
`testTimeout: 60000`, and Testing Library `asyncUtilTimeout: 10000`. Explicit
per-test deadlines, assertions and coverage were unchanged. Both configuration
and setup files were restored byte-for-byte afterward and have no Git diff.
This is a local timing accommodation, not a default-configuration pass or a
shipped test-policy change. The machine's observed load averages exceeded 200.

`git diff --check` passed. Temporary preview source/configuration and both
agent-generated repository `dist` directories were removed before committing.
The original checkout's pre-existing parent AGENTS modification and deleted icon
were preserved. No Rust production source changed, so the repository's
Rust-change-triggered root `cargo test` requirement did not apply.

## Remaining acceptance limits

Browser/component/build results do not establish packaged CEF desktop behavior,
real provider/account operation, native macOS/Windows/X11 viewport or keyboard
acceptance, or true browser/native 200% zoom. Those native checks were not performed
for this frontend change. No production release, signing, deployment or provider
readiness is claimed. The unchanged native widget/package/launcher fixtures are
reported separately from actual application runtime acceptance.
