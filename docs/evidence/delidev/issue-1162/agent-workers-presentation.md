# Agent Workers presentation evidence

Issue: https://github.com/delinoio/oss/issues/1162. Observed on 2026-09-30 on macOS. The isolated implementation branch is `kdy1/delidev-agent-settings-1162`, based on `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` from `origin/main`. This record describes source and synthetic-fixture validation, not deployment or native acceptance.

Validated source SHA-256:

| Source | SHA-256 |
| --- | --- |
| `apps/delidev/src/settings.tsx` | `f63de4ead15a9e347a02719698cb012dd291647d7e7411b927fbb7cd885fa6e7` |
| `apps/delidev/src/styles.css` | `64ea41b402eae72ccef9596a8cf9f1ee4f207b7eab056781d44a07503726e681` |
| `apps/delidev/src/settings.test.tsx` | `72fb09c07302a04333a7d781de18c121e6881c3a79f43a888918177c5fd83df1` |
| `apps/delidev/src/settings-lifetime.test.tsx` | `0e2550665e1eea556c8312b1dc1d01068f3d7957fd4e4231085b8176b8ed66a6` |

## Package validation

- `pnpm install --frozen-lockfile` succeeded in the managed worktree, including shared-directory Lefthook installation.
- `pnpm --dir apps/delidev prepare:assets` restored the exact required LFS icon from local cached objects. `git lfs fsck` passed.
- A focused `pnpm exec vitest run src/settings.test.tsx src/settings-lifetime.test.tsx` passed 48 tests before adding the two additional Agent Close/cancel parameter cases. Those additional cases passed in the final full run.
- `pnpm --dir apps/delidev test` completed with exit 0: generated API-client build, TypeScript typecheck, 84 Vitest files/976 tests, eight native-package/bundle fixtures, sixteen asset/desktop-launch fixtures, Swift widget fixtures and the production Rsbuild build all passed.
- That final package invocation temporarily used `maxWorkers: 1`, `testTimeout: 30000`, `hookTimeout: 30000`, and Testing Library `asyncUtilTimeout: 30000`. Assertions and production code were unchanged. Both temporary configuration edits were restored byte-for-byte before committing. The initial default-worker invocation failed with timeouts across 15 files (50 tests failed, 923 passed, one skipped) while other worktrees' test processes were active; a single-worker run with the original short waits also timed out and was stopped. The final result establishes the bounded serial run, not a passing default-parallel run.
- `git diff --check` passed. Generated repository-owned `dist` directories and temporary browser entries were removed before committing. No Rust source was changed.

The generated Connect router fixtures cover successful empty inventory, delayed loading, initial PermissionDenied/Unavailable errors, continuation and later-empty pages with exact AGENT/50/token requests, cached-empty and populated refresh failure, full inert names/IDs, supplied metadata only, action order and schema gates, exact routing/editor targets, revision conflicts, revision-7 deletion uncertainty/retry, and save request identity across reflow and same-identity transport replacement. Existing routing, category inventory and targeted-entry tests remain in the full suite.

The opening-lifetime fixtures additionally cover Agent and Instructions Close/cancel disposal and an Agent save accepted before acknowledgment whose transport ignores abort and later succeeds or returns Unavailable/Canceled. They assert signal/cache cleanup, retained sibling cache/composer, fresh AI Subscription reopening, no late focus/category/retry leak, one save only and visibility of the committed record through a fresh read.

## Browser geometry and keyboard observations

A temporary production Rsbuild entry rendered the real Settings component and production CSS with an in-memory generated Connect router, synthetic empty/long/schema-2 Agent resources and a sibling composer. It used no credentials or real server writes. The entry and loopback server were removed after inspection. Both empty and long-row fixtures were measured in the Codex in-app Chromium browser at all eight viewports below; none had horizontal overflow.

| CSS viewport | Agent column width / left | Content padding | Toolbar | Compact selector | Empty minimum |
| --- | --- | --- | --- | --- | --- |
| 1728×1056 | 1040 / 464 | 40 | Beside title | Hidden | 360 |
| 1440×900 | 1040 / 320 | 40 | Beside title | Hidden | 360 |
| 1024×768 | 736 / 264 | 24 | Below title | Hidden | 360 |
| 640×480 | 608 / 16 | 16 horizontal | Below title | Visible | 280 |
| 1100×768 | 780 / 280 | 40 | Beside title | Hidden | 360 |
| 1099×768 | 811 / 264 | 24 | Below title | Hidden | 360 |
| 760×640 | 472 / 264 | 24 | Below title | Hidden | 360 |
| 759×640 | 727 / 16 | 16 horizontal | Below title | Visible | 280 |

The shared header remained 64px high and the sidebar 240px wide where visible. At both wide viewports the 1040px column had equal pane gutters. Computed title/summary/scope sizes were 26/14/12px; the empty heading/copy were 20/14px, the decorative tile 56px, panel fill/border `#fafbfc`/`#d8dee8`, panel radius 12px, and primary control fill/height/radius `#2563d8`/40px/8px. Empty-panel content and long rows could grow vertically with the existing pane scroll.

Actual Google Chrome zoom was verified through its native accessibility surface at 200%. Long literal-markup names stayed inert and wrapped, complete IDs remained visible, actions wrapped beneath the row and the focused Edit control retained its visible unclipped outline. The toolbar moved below the title at the resulting CSS width. This was a disposable fixture tab; existing user tabs were preserved.

Browser keyboard inspection confirmed Escape and Close returned focus to the connected outer opener. Tab from Close entered the category navigation. At the reverse boundary Chromium temporarily reported document/body focus rather than a modal element; the background opener/composer did not receive focus. This observation is not a claim that every intermediate focus position remained inside the dialog. A new Agent draft survived 1440-to-640 reflow, retained the compact navigation lock and hid the parent toolbar. Escape/reopen started at AI Subscription with no recovered editor or draft; a fresh Agent form was empty and the sibling composer remained intact.

## Native evidence limits

No prepared native CEF launch against an explicitly selected valid server was performed for this revision. An existing DeliDev instance was active and an isolated native runtime/state setup was not established during this run. Native content viewport, Tab/Shift+Tab containment, background inertness, native Escape/Close opener restoration and selected-conversation preservation therefore remain unverified. Browser and package fixtures do not substitute for that acceptance. Windows and Ubuntu X11 environments were unavailable on this macOS host; no native acceptance is claimed for them. The 640×480 result is browser/CSS evidence only and does not change the native 960×640 minimum.
