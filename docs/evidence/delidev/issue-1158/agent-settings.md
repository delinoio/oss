# Agent Worker core and optional settings evidence

Issue: [#1158](https://github.com/delinoio/oss/issues/1158). Observed on
2026-09-30, starting from freshly fetched `main` at
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. This record covers the Agent
presentation change on `kdy1/issue-1158-agent-settings`; it does not establish
complete DeliDev or supported-platform native acceptance.

## Implementation and regression boundary

Create and edit share the existing configuration controller with an Agent-only
presentation component and stylesheet. Core controls and permissions stay visible;
Reasoning, Accounts & routing, Instructions and Native harness options use four
independent initially closed native disclosures. Their children stay mounted.
Summaries reflect retained values, including unknown options, and expose read or
validation problems. Native invalid-input handling reveals the first invalid
control before focusing it. API requests, schema/defaults, mutation lifecycle,
Settings shell and native window behavior are unchanged.

The generated Connect router fixtures run under React Strict Mode. Nineteen new
tests cover minimal accountless creation; full-document/unknown-field and revision
preservation; ordered account/template operations and weights; bounded pagination
and exact off-page identities; loading, empty pages with continuation, denied/auth
and connection reads, failed cached refresh and model-only capability failure
scope; hidden invalid weights/concurrency; harness/permission retention and explicit
clearing; unsupported selections; original uncertain retry; revision conflicts;
Close/native cancel/navigation disposal and ignored late accepted responses.
The existing real temporary Go-server configuration and Claude tests remain in the
focused verification set. No user credential, installed harness, external account
or inference endpoint was used.

## Executed checks

Commands below ran from `apps/delidev` unless specified otherwise. Generated client
inputs were built first. Repository LFS objects were hydrated; root
`git lfs fsck` reported `OK`. Root `pnpm install --frozen-lockfile` succeeded and
installed the shared-worktree hooks.

| Check | Observed result |
| --- | --- |
| `pnpm typecheck` | Passed after the final source change |
| `pnpm exec vitest run src/agent-configuration.test.tsx src/settings.test.tsx src/settings-lifetime.test.tsx src/settings-configuration.integration.test.tsx src/settings-claude.integration.test.tsx --maxWorkers=1 --testTimeout=30000` | 5 files, 55 tests passed, including all 19 new Agent cases |
| Required `pnpm test` | Failed in Vitest: 75 files / 958 tests passed; 10 files / 18 tests failed, primarily short asynchronous deadlines |
| `VITEST_MAX_WORKERS=1 pnpm test` | Failed in Vitest: 80 files / 960 tests passed; 5 files / 16 tests failed; package command did not reach later stages |
| `pnpm exec vitest run --maxWorkers=1 --testTimeout=30000` | 82 files / 975 tests passed; 3 unchanged Go-backed Settings integration tests failed at asynchronous element waits |
| `pnpm test:bundle-dry-run` | 8 checks passed, separately executed after the package test stopped |
| `pnpm test:desktop-launch` | 16 launcher/asset checks passed |
| `pnpm test:widget` | Swift widget fixture checks passed |
| `pnpm build` | Production frontend build passed |
| Root `git diff --check` | Passed |

The three extended-run failures were
`settings-devices.integration.test.tsx` (paired-client revoke lookup),
`settings-preferences.integration.test.tsx` (New Server preferences lookup), and
`settings-workspace.integration.test.tsx` (Delete Owned checkout lookup). Those
files and their product paths were not changed by this work. The host was also
running other repository build/test tasks. This observation alone does not prove
that every failed test is a baseline failure or that the required package test
passes.

A separate exact-main source snapshot at the inspected revision reproduced the
default-deadline failures in `App.test.tsx` (notification/import disposal) and
`tray-presentation.test.tsx` (native-selected view): 36 tests passed and 2 failed.
These are baseline evidence for those two cases only. The complete package test
remains a recorded validation gap; test defaults were not changed to hide it.

An additional exact-main run of the three remaining integration files passed the
workspace case. Device/preferences fixture setup failed before executing their
tests because the shared Go build cache's linker input files were missing. That
attempt does not establish baseline pass/fail for either case, and the cache was
not cleared or another task's processes altered to force a result.

Before publication, main at `7f356266fc195b1880ffac66a93dadab5c5a2df7` was merged
as `9804dfb4`, retaining #1178's AI API Keys presentation and #1179's Diagnostics.
The shared-editor and desktop-contract conflicts were resolved by preserving both
changes. Generated client build and `pnpm typecheck` passed again. The focused
command above plus `src/doctor.test.tsx` and `src/account-settings.test.tsx` passed
all 7 files / 106 tests on that combined revision, followed by a passing production
`pnpm build`. This focused result does not supersede the complete-suite/native gaps.

## Browser layout and keyboard observations

A disposable loopback browser fixture rendered the production Settings dialog and
Agent editor with generated Connect router responses and fake public resources.
It used the existing shell and styles, with no server/native execution. The
fixture, configuration, preview server and generated repository-owned `dist`
directories were removed after verification.

| Browser viewport in CSS px | Measured form / columns | Horizontal content extent |
| --- | --- | --- |
| 1440 × 1000 | 800px form; equal 367px controls | 1200px client and scroll widths |
| 1280 × 820 | 800px form; equal 367px controls | 1040px client and scroll widths |
| 960 × 640 | 672px form; equal 303px controls | 720px client and scroll widths |
| 920 × 640 | 632px form; one 582px column | 680px client and scroll widths |

The form stayed aligned to the existing content edge. The footer had static
positioning and participated in vertical scrolling; it was not forced into the
initial viewport. At 200% root text size, descriptions and summaries wrapped and
the content's client/scroll widths remained equal. This was text-size reflow,
not a native 200% zoom acceptance run.

Browser keyboard checks observed Enter/Space disclosure toggles, Tab moving
directly between closed summaries without entering hidden controls, and an invalid
concurrency of 65 revealing/focusing its native input without save or unrelated
draft loss. Escape closed the native HTML dialog and restored focus to its opener;
reopening created a fresh empty draft with closed disclosures. Populated edit
showed the retained effort and customized native summary with all disclosures
initially closed. Component tests separately covered invalid weights and every
disposal route. Browser viewport and fixture results are not native CEF evidence.

## Native build and remaining limits

On macOS, `CARGO_TARGET_DIR=<shared-target> pnpm dev:desktop -- --help` completed
the source-asset preflight, Go sidecar, widget preparation and pinned CEF desktop
host compilation (`Finished dev profile`, 7m45s). The wrapper then blocked during
the bundle/run stage on a shared Cargo artifact lock held by another task. Only
this attempt's wrapper was terminated; the other task's native process was left
running. No native window was launched or accepted by this run. The `--help`
argument was intended to avoid opening real user state after packaging, not to
exercise product runtime behavior.

Native create/edit, expanded/error rendering, focus containment and 200% zoom
remain unverified on macOS CEF. Windows and Linux native viewport/keyboard checks
were not performed. The native compilation, portable browser checks and passing
focused tests must not be promoted to cross-platform acceptance. No Rust source,
native geometry, protocol, dependency, credential behavior or execution support
was changed.
