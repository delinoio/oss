# DeliDev parallel browser QA

## Scope

`apps/delidev/scripts/qa/` owns the local QA launcher, bounded HTTP host and
disposable-environment cleanup. `apps/delidev/src/qa/` owns a separate browser
entry using the existing product App. Neither entry is a shipped browser product.
The desktop host, product RPCs, schema allocations and migrations remain owned
by their existing contracts.

## Runtime and Language

Use the repository Node.js/pnpm runtime, JavaScript tooling, Rsbuild and the
existing React/TypeScript frontend. Compile the ordinary Go `delidev` executable
once per run. No CEF build, virtualization or extra product dependency is needed.

## Users and Operators

Maintainers and explicitly authorized MainQA coordinators use this development
tool. Starting it does not authorize a QA agent to use a hosted account, alter an
external repository or issue a production mutation.

## Interfaces and Contracts

Run from the repository root:

```sh
pnpm --filter delidev-desktop dev:qa -- --workers 4
```

The default is four environments; accept integers 1–8 only. Build the QA entry
once without a watcher. Give every environment its own loopback frontend origin,
Go server listener, server UUID, paired client, paired Worker and private state.
Use existing `server run --listen 127.0.0.1:0`, exact `--allowed-origins`,
`device pair-local`, `worker pair-local` and foreground `worker start` commands.
Readiness requires authenticated server/version checks and a connected original
Worker, separately from installed harness or account readiness.

The local `/__qa/` routes expose bootstrap, server status/explicit Start and original
Worker registration/lifecycle/proof/network controls. The separate browser entry
injects an environment-local appearance adapter. These are internal QA interfaces,
not Connect procedures.
They accept closed actions and never executable, scope, endpoint or argv input.
Worker Stop retains its exact original generation. Concurrent mutations are
rejected while the original operation runs; reads never start or repair a child.
Same-identity reconnection retains the existing App and pending product intents.

The browser calls real product Connect RPCs directly. Use the ordinary App,
generated Connect Query services, styles and connection-scoped caches. Do not
fake product responses or native authority. File selection uses the existing
manual path alternative. Trusted-window OAuth callbacks, tray, OS notifications,
CEF child views, saved native windows and desktop installation remain unavailable
and require separate native acceptance. Appearance QA uses isolated local state,
not the native app's saved appearance.

The launcher prints a sanitized run manifest containing worker IDs/URLs, original
source revision, dirty-source indicator, state and evidence location. MainQA may
assign each ready URL to one worker; its model, capacity, recording and coverage
rules remain unchanged. An unavailable native operation is blocked coverage,
never a passing browser check. The external MainQA skill is not edited here.

After the `qa-environments` record lists `ready` environments, give MainQA the
command, its JSON record and the requested coverage. For example:

```text
Use main-qa against the DeliDev browser QA environments started with:
pnpm --filter delidev-desktop dev:qa -- --workers 4
Assign each ready environment URL from the attached qa-environments JSON to
one QA worker. Record screenshots under its artifacts/screenshots directory.
Use the existing repository path input with an explicitly selected test checkout.
Treat the listed native limits as blocked coverage and report them separately.
```

Keep the launcher running until QA workers finish. Each worker uses its own URL,
including in tabs of one Chrome profile. Separate ports isolate origin storage;
the appearance key additionally contains the original server ID. A stopped server
keeps the same endpoint and identity on explicit Start. Starting a server does
not replace a stopped Worker, pair a revoked client, or certify harness readiness.
Server controls remain in the QA banner across Settings navigation. Reloading a
stopped environment retains an explicit Start action, while product screens wait
for renderer-side authenticated identity/version verification. Once verified,
same-identity disconnections retain the existing App and pending intents.

### apps/delidev constraints

- Parallel browser QA follows `apps-delidev-qa-contract.md`. Keep its entry and host outside product builds, use real per-environment Go servers/Workers, and preserve pending product/credential cleanup before deleting owned temporary state. Browser evidence cannot establish native-window or real-account acceptance.

### apps/delidev/scripts constraints

- `test-claude-runners-layout.mjs` validates bounded Claude Runner exclusion, read failure and partial/stale/malformed evidence in English/Korean and narrow/zoom layouts. Preserve original eligibility and inspection counters; fixture checks do not establish native installation or real-account acceptance.

- `test-session-actions-layout.mjs` validates synthetic sidebar execution colors, sibling menu geometry, no-navigation opening and keyboard/focus in English/Korean, light/dark and wide/narrow/effective 200% viewports. Build/server/browser state stays outside the checkout and is disposed. Use host Playwright/Chrome; the fixture grants no native CEF, account or platform acceptance.

- `test-account-format-layout.mjs` validates the synthetic capability-9 edit dialog at wide/narrow viewports, native labeled select, Provider lock, key absence, footer/body scrolling and keyboard focus/closure. Keep screenshots outside the checkout and native-select keystroke/native/account acceptance limits explicit.

- `test-subscription-cleanup-layout.mjs` validates the synthetic Settings fixture in English/Korean, light/dark and wide/narrow layouts with keyboard activation and focus preservation. Keep outputs outside the checkout and separate browser fixture evidence from native/account acceptance.

- `qa/` owns the explicit parallel browser QA launcher and host under `apps-delidev-qa-contract.md`. Track preparation, lifecycle and cleanup children, pin every browser control to its original private environment, and keep credentials out of logs/manifests. Delete state only after original product cleanup and process exit; preserve uncertainty and evidence outside the checkout.

- macOS `dev:desktop` must use `tauri.conf.json`'s `bundle.macOS.minimumSystemVersion` as `MACOSX_DEPLOYMENT_TARGET` for both Cargo preparation and Tauri bundling. Do not alternate an ambient/default deployment target with the configured target in the shared Cargo cache.

- Icon export validation must inspect the final PNG and every ICO frame for near-opaque ribbon interiors, graded edge alpha, transparent backgrounds and centers, and normalized alpha coverage within one percentage point of the canonical source. Composite the final exported PNG over light, dark, and checkerboard backgrounds; checking only transparent pixels or an intermediate image cannot establish a valid export.

- The desktop sidebar uses one 52px icon rail and a 288px context pane (256px at viewport widths up to 1,100px). At widths below 760px retain the rail and expose one native modal drawer with contained focus, Escape/Close, and opener restoration; keep the same controller and query instances while the pane moves. Keep DeliDev static, Inbox/Search in its header, and the status/local-server footer outside the independently scrolling content. Use direct authenticated Connect Query for independent bounded catalog pages. The Sessions context groups by original `Resource.projectId`, retains collapsed groups and selection in connection memory, and resets only session cursors when archive visibility changes. Keep the selected conversation mounted. The Projects heading's icon-only **New project** button opens the existing Projects Settings form, retains its editor and exact uncertain save through navigation within one Settings opening, and waits behind protected parent/nested workflows. A successful save invalidates active queries without resetting sidebar cursors. Each failed session-navigation query has a Retry for its same current query/page; it cannot reset cursors or invoke writes.

- Every continuation owns fresh job/execution/process/runtime/outbox/credential identities. Reuse only the validated original private `CODEX_HOME` after the exact predecessor lease/checkpoint and native history/defaults checks; never rebuild missing evidence, adopt a new thread or resend prior input. Publication/relay/completion/control must target current selection, reject old authority and preserve exact prior receipt semantics. Check every retained terminal inbox source before binding a new native turn. Goal-absence resume metadata is content-free and grants no execution authority; populated/foreign goals remain unsupported until their own adapter. Keep stable typed publication-failure diagnostics without raw native content.

- Claude manual-command process replacement must pin each originally verified closed transcript as an exact immutable prefix. Retain synthetic Resume context separately from input, output and accounting; require its exact native shape, original parent and next-input ancestry. Only that proved successor and an original failed action prefix may retain its unchanged local-command diagnostic outside the newly selected context. Never derive failure or Resume authority from human text, rebuild missing prior input, rewrite native bytes or serialize this live capability as crash-recovery authority.

- Grok Build discovery requires its pinned private ACP profile and a preceding owned read-only native configuration inspection. Reject inherited project/managed/remote settings and external extensions before ACP; never bypass machine policy. Reconstruct empty private homes and documented compatibility/update settings, advertise no client execution capabilities, and send only initialize. Require its original correlated response plus the exact empty startup MCP inventory, bound and join both streams/writer/owned cleanup, and discard private native metadata. Authentication, sessions, prompts and callback replies require separate adapters; a source-hash version suffix or handshake cannot grant execution/account readiness.

- OpenCode primary selection is the closed native Build/Plan type. Compare the selected complete agent descriptor and ordered permissions, retaining Plan edit denial, native plan-file exceptions and independently rooted relative paths without interpreting globs or asserting an OS sandbox. Explicit session rules stay separate native overrides. Preserve selected agent through original session/input/assistant evidence; Plan exit approval, switching and child execution require their own adapters and cannot be inferred from Plan text completion.

- Registered OpenCode relay authority is limited to the pinned first-input Chat Completions profile and exact supported Build/Plan settings. Reuse every current job/input/session/account/Worker/epoch check, refuse protocol translation or unsupported explicit options, and cancel/join provider requests before protected-key deletion. Actual native registration evidence cannot enable public dispatch, reconstruct native acceptance or substitute relay cleanup for original process cleanup.

- The original OpenCode binding coordinator owns one fresh journal and must acknowledge session publication before allowing the native first-input claim. Closed or failed writers cannot regain authority from unchanged files. Close original native resources before the coordinator and shared publisher; retained journals never grant a replacement runtime. The server accepts pinned first-session/default Build/Plan binding, stored-input acceptance and separately integrated original text-part publication; late original facts preserve Stop/recovery and cannot restore relay access.

- OpenCode Worker execution consumes an authenticated immutable first assignment or an exact version-2 checkpoint continuation, the selected executable and the independently owned workspace lease. Keep registration intent in the Worker job journal outside the fresh empty native runtime. Preserve a bounded original event prefix until live and stored input acceptance, then publish it once. The outbound stream owns native lifetime; targeted cancellation before acceptance terminates startup, while an accepted live input uses one bounded original abort and joined response controls. Owner-only cleanup cannot invent terminal reporting. Close native history/process, retain the separate original Worker checkpoint while holding workspace ownership through any original snapshot export, then close the workspace lease before durable reporting. Only the independently eligible Build/Plan General Chat or single-root Git snapshot and closed inline tool/interaction profiles defined below may produce a new version-2 report; the private file alone grants no later input or resumed process authority. Public first dispatch uses the same immutable OpenCode selection gate, exact pinned native protocol and Chat Completions provider/account authority. Multi-repository settings and Windows non-VCS root identity require their separate profiles; do not drop explicit roots/options. General Chat must independently exclude enclosing Git metadata.

- Private OpenCode process replacement requires an independently retained original closed checkpoint, fresh process/runtime/credential ownership and one durable predecessor-bound resume claim before native staging or launch. The supported profile is Build/Plan non-VCS text/reasoning plus the positively observed closed inline tool/interaction profiles defined below; do not adopt other tool, auxiliary or child state through it; single-root Git history additionally requires the positive snapshot profile below. Copy exact SQLite database/WAL/SHM bytes into the new private runtime without mutating the predecessor or importing old configuration/account files. Revalidate effective settings, sole original session, idle/pending inventories and all original ordered message/part digests before permitting one fresh input. Preserve full bounded lineage, forbid old request/message/part reuse and require explicit intent after Stop/failure. Temporary read authority ends on every path; history contradictions latch failure and cleanup uncertainty takes precedence. Public first-assignment journals still reject this private resume claim, and version-1 reports cannot grant continuation.

- OpenCode queued Plan/Execute changes use the native per-input Build/Plan selector in the same original session. Require the preceding agent from the immutable predecessor assignment and compare its complete settings digest before staging or historical reads; only that selector may differ from the newly requested profile. Model/account/permission/instruction/relay authority stays fixed. Validate both old and new observations against their own queued modes and preserve original progress. Native transition reminders remain native output, never injected DeliDev prompts or a synthetic execution approval.

- OpenCode tool-proof version 3 retains direct confirmed Read `always` claims and exact original ordered allowance patterns under the independently verified default Build/Plan policies and empty base session permission list. Keep the original native acceptance order and the exact already-applied prefix. The durable predecessor resume claim owns one native PATCH append of only the unapplied suffix after complete historical validation; validate its response and fresh metadata, then recompare the entire history before input. Never replay a lost/uncertain append, rewrite predecessor files, re-send original approval, interpret wildcard patterns or widen another permission. Keep immutable configuration distinct from restored native session permission metadata in both session reads and live notifications. Other remembered permissions and unsupported auxiliary state remain gated; automatic Read cascades use the separate original-policy proof below.

- OpenCode tool-proof version 13 retains original external-directory remembered allowances for eligible Read/Shell/Glob/Grep/Write/Edit/Apply Patch parts, independently of each tool's own permission. Require original direct always acceptance or separate automatic closure/source evidence, exact native rule order, one permission name per original approval and the existing bounded applied-prefix restoration. A typed optional automatic permission name preserves omitted historical Read meaning; automatic closures require an observed same-permission direct source without guessing wildcard attribution. Versions 1–12 cannot acquire external authority. Preserve pinned Build/Plan policies and empty base session rules; never infer edit/bash/search allowances, replay an old reply/file operation or retry an uncertain native PATCH. Keep original error/rejection/Question/Todo proofs and read-only paused recovery intact; logs contain counts and phases only.

- OpenCode multiple-repository execution follows the native local-reference contract. Preserve the manifest's primary cwd and ordered additional repository UUID/path pairs, require disjoint canonical paths without unverified glob/interpolation spellings, and retain the complete workspace lease. The pinned dual-loader bridge may create only an exclusive private reference-only config with its fixed schema; global config remains empty and v2-visible project sources are refused. Compare full effective config and native Build/Plan permissions, recheck exact private bytes and paths, and bind ordered references in checkpoint metadata/settings and read-only recovery. Never clone remote references, invent a common root, restore secondary files from the primary snapshot or omit selected repositories.

- `test-settings-layout.mjs` builds the synthetic `src/settings-layout.fixture.tsx` entry into an isolated temporary directory and checks the actual shared app/Settings in host-supplied Playwright Chromium. No product dependency, native authority, real account/state, external RPC or committed evidence is introduced. `DELIDEV_LAYOUT_PLAYWRIGHT_MODULE` optionally selects the host module; `DELIDEV_LAYOUT_BROWSER_CHANNEL` optionally selects an installed browser. Keep all 18 categories, light/dark/System, empty/populated inventories, viewport/form/task-dialog checks (bounds, fixed actions, one modal surface except the explicit GitHub repository child chooser, backdrop retention, keyboard containment and opener restoration) and effective 200% coverage, dispose browser/server/temp outputs, and report actual native/chrome zoom acceptance separately.

- Harness layout checks in that fixture cover the four-card geometry, 640px available-width transition, decorative local images with transparent contain boxes and local masks, single selection and button focus. Arrows/Home/End and form submission cannot advance; native Space/Enter and current-card clicks open Accounts and focus its heading without skipping a step. Harness has no Next button. `DELIDEV_LAYOUT_SCREENSHOT_DIR` and `DELIDEV_LAYOUT_SCREENSHOT` optionally save synthetic screenshots outside the repository; keep screenshots separate from disposable bundles and record native acceptance as unperformed.

- `test-toast-layout.mjs` uses the synthetic Settings fixture and host-supplied Playwright to validate the real App toast/save flow, production-style static CSS CSP, themes, effective narrow/zoom geometry, compact drawer timer suspension and keyboard dismissal. Keep generated bundles and optional screenshots outside the repository, dispose temporary servers/browser/output, and distinguish these checks from native CEF/platform acceptance.

- `test-session-hover-layout.mjs` uses that fixture's opt-in synthetic session inventory and host-supplied Playwright to verify the real sidebar hover card in English/Korean, both themes, ordinary/narrow viewports, effective 200% zoom, measured placement, full names/reasons, pointer-gap retention, keyboard dismissal, modal top-layer clipping and static CSS CSP. Keep optional `DELIDEV_HOVER_SCREENSHOT_DIR` output outside the checkout, dispose temporary browsers/servers/bundles and record native CEF/platform acceptance separately.

- Settings uses the four ordered groups AI, Coding, Device management and System with 18 independent category screens under the desktop contract. Git Profiles retains `integrations`; legacy `git-workflow` entries redirect to Project defaults without a separate navigation or search destination. Project defaults owns account routing, global Worktree fetch, PR remediation and session creation policies through the same SETTINGS singleton, complete documents, defaults, read/revision/create guards and original uncertain requests. Server preferences owns only its independent Network settings and never reads or edits the policy singleton. Repository overrides and category disposal remain unchanged.

- `test-repository-dialog-layout.mjs` covers the GitHub chooser above the retained 640px Add repository task using synthetic revision-bound GitHub replies. Preserve English/Korean, light/dark, six viewports and effective 200% checks, parent inertness, child-only Escape/X, independent body scrolling, opener focus and draft/selection retention. Nonmodal sidebar regions do not count as open modals. Keep fixture screenshots and native CEF acceptance separate.

- `DELIDEV_LAYOUT_DISMISSAL_ONLY=1` scopes the Settings browser fixture to safe destructive header focus/Enter, keyboard containment, opener return and duplicate removal in English/Korean, both themes and wide/narrow/effective 200% layouts. It runs no native or account acceptance.

- `test-jobs-layout.mjs` and `src/jobs-layout.fixture.tsx` check synthetic original-job polling and success-notice removal in host Playwright/Chrome. Keep English/Korean, light/dark, narrow/wide bounds and keyboard/focus checks, feature children and no visible/accessibility operation IDs. Temporary browser/build/server output stays outside the checkout and is disposed. Fixtures grant no native/account acceptance or product authority.

- Concurrent localization preparation publishes each generated compiler input through unique same-directory atomic replacement and skips unchanged output. Never truncate a live catalog; keep staging files disposable and remove them after failure.

- Server preferences Network settings is an initially collapsed inline workspace under the desktop contract. Mount a fresh plain SettingsLifetime and MutationIntents per opening, without the task-scope marker; child profile dialogs own independent scopes. Expansion mounts only the independent Network workspace, without mounting or reading the Project defaults policy singleton. Preserve authoritative route/decimal generation reads, explicit native profile selection and current bounded scrolling. Outer collapse disposes children, secrets and exact retries without canceling accepted effects; inner Worker transfer stays mounted and reveals hidden invalid controls before focus. Runner Device network retains its modal ownership. Preserve English/Korean, semantic themes, reflow and separate browser/native evidence.

- Claude Runner layout checks assert that selected and excluded choices expose no Inspect this Runner shortcut or shortcut-owned dialog in either language. Preserve eligibility, exact selected reads, pagination/refresh, compact/full keyboard access and the shared original inspection gates; explicit Runner Devices diagnostics and original session failure recovery keep their authority. Browser fixtures remain separate from native/account acceptance.

- `test-inbox-filter-layout.mjs` checks immediate Inbox request conditions, native keyboard selection and retained compact drawer/focus across Reset in English/Korean, semantic themes and effective 200% layouts. Record this as synthetic browser evidence, separate from native platform acceptance.

- `DELIDEV_LAYOUT_PROJECT_ROWS_ONLY=1` scopes the existing Settings fixture to the feature saved-ID singleton/multi/zero Projects rows. Validate exact matching-name suppression, inert complete URL wrapping, borderless keyboard identity disclosures, fourth-primary Show all/focus, bilingual themes and 960px/effective-200% reflow without disclosure/reflow reads or writes. Save synthetic screenshots outside the checkout; fixtures grant no native/account/platform acceptance.

- `test-connections-layout.mjs` owns isolated Connections browser geometry in English/Korean and light/dark themes at wide, compact and effective 200% widths. Keep `__connectionsFixture` outside product bundles and build/browser/screenshots outside tracked source. Assert saved inventory does not imply remote readiness, Advanced/menu opening adds no mutations, private pairing stays masked and dialog/menu focus returns to the original opener. This grants no installed-native, real-account or platform acceptance.

- `test-provider-refresh-layout.mjs` uses the existing Settings synthetic fixture with a held identical provider response to measure unchanged row positions before, during and after refresh. The opt-in browser gate grants no mutation or native authority; cover both locales/themes and desktop/compact widths and dispose temporary bundles, browser and server.

- Settings layout validation offers an opt-in synthetic Accounts wizard matrix for both generations, English/Korean, themes, responsive/effective-zoom widths, source grips, fixed footer and complete-empty links. Fixture-only query invalidation verifies removed refresh controls without adding product authority. Keep screenshots outside the checkout and distinguish browser fixtures from native/account acceptance.

- Routing preview layout checks measure badge, Project control and Refresh centers within 1 CSS pixel under wide idle, held-refresh and selected-project states. The opt-in synthetic routing gate grants no execution authority; preserve localized labels, compact stacking, request counts, disposal and effective-zoom evidence boundaries.

- Creation image layout checks also validate the localized non-submit plus immediately before Agent Worker, pointer/Enter/Space picker activation, absent history/limits/help presentation and independent General Chat toolbar. Retain original image decoding, ordered admission/removal, paste/drop, draft ownership and receipt checks; no fixture grants native/account acceptance.

- `test-repository-spacing-layout.mjs` uses the existing isolated pagination fixture with real RepositoryRow and ScrollPayloadWindow components. Verify 16px within-page and cross-page card gaps, measured eviction placeholders, restored scroll/focus and repository-only styling in English/Korean, themes and compact/effective-200% layouts. Keep temporary build/browser/server state disposable; no screenshots or native/account operations are required. Browser evidence grants no installed-platform acceptance.

- `test-notifications-layout.mjs` reuses the Settings fixture with an isolated Notifications branch and synthetic native-status adapter. Verify saved-section/editor width distinction, action/value edges, single separators, 24px gaps/16px rows, narrow/effective 200% reflow, six permission states, explicit reads and unchanged editor focus/no-save behavior. Keep all build/server/browser output disposable and outside the checkout; do not capture screenshots or claim native/platform/account acceptance.

- `test-agent-permissions-layout.mjs` uses isolated creation/edit permission fixtures with the real shared permission component. Check local choices, original warnings/values and shared-picker geometry, themes, keyboard/focus and locks; dispose fixtures and never claim native/account acceptance.

- Session header/Info fixtures follow the feature: compact retained identity/live connection/actions only, independent technical/title evidence in labelled Status rows, six initially expanded primary sections and single reveal gates. Retain resource-tab, child identity, original input/controller/focus, narrow/short-height and native-lifetime assertions; fixture presentation grants no installed/native/account acceptance.

- `test-tool-turn-layout.mjs` uses synthetic shared transcript/renderer fixtures for compact tool groups, original output, nested disclosure state, revisions/focus, exact page restoration, locale, themes, narrow reflow and owner disposal. Session/General Chat fixture labels exercise their shared presentation, not native session admission. Synthetic adjacent question/composer fields check presentation separation; real request/admission tests retain authority coverage. Temporary bundles/browser state remain outside source; no screenshots or native/account acceptance.

- `test-creation-skill-layout.mjs` checks real project/General Chat creation with synthetic skill inventory in both languages/themes and narrow reflow. Preserve row geometry, keyboard selection and ancestor scroll; dispose isolated bundles/server/browser and keep fixture evidence separate from native/account acceptance.

- Usage session-table regression uses actual native wheel input with the real built stylesheet: no-overflow chaining in both directions, inner-before-parent overflow boundaries, horizontal/keyboard scrolling and an original-containment counterfactual. Fixture-only bounds/rows are removed after checks; filters, tabs, disclosures and reads remain unchanged.

### apps/delidev/src constraints

- `qa/` owns only the separate browser QA entry and scoped adapters under `apps-delidev-qa-contract.md`. Reuse App/Connect Query and connection memory, pass only paired client authority, and never spoof Tauri or CEF. Native-only controls remain unavailable before side effects; QA theme storage cannot touch native appearance.

## Storage

Place generated binaries, frontend assets and every server/client/Worker scope
in owner-private temporary directories outside the checkout. New environments
start without user configuration or AI credentials. Keep screenshots and run
manifests separate from deletable environment state. Never track generated `dist`
or validation evidence in the repository.

On shutdown, close browser control admission, drain in-flight operations, and
request existing product session/protected-resource cleanup while the original
Worker remains available. Retain exact request identities while observing work.
Stop and reap only this run's children. Remove an environment only after both
product cleanup and original process exit are confirmed. Unavailable server
authority, unsettled jobs/native ownership, protected references, malformed
metadata or failed process cleanup preserve that environment and report a stable
reason and its recovery location. Killing a process is not native cleanup proof.

Use Ctrl+C or SIGTERM for normal shutdown. The final `qa-cleanup` record and
`artifacts/run.json` report `deleted` or `preserved` per environment. A preserved
record includes its original state path, server identity, endpoint, frontend origin
and stable reasons; the shared binary remains in the parent run directory. Keep
these private scopes intact. Use that binary with the retained `--data-dir` and
`--worker-dir` for product inspection/recovery, never a personal scope. The runner
may start a stopped owned server at its original endpoint for cleanup after browser
admission is closed; it does not silently start a stopped Worker. A protected Worker
network recipient without a product removal operation remains preserved, even
when ordinary metadata work finished. An externally installed or replaced Worker
also retains ownership for separate reconciliation. Do not delete such directories
on the basis of process exit. Artifacts remain separate and are not removed.

Metadata/Git inspection needs no AI account. Actual AI execution requires an
explicitly configured test account and an installed supported harness. The tool
does not copy personal DeliDev configuration or account material.

## Security

Listen only on numeric IPv4 loopback. Each Go server allows only its own QA origin.
The QA host validates Host and Origin, rejects cross-origin control, and uses
no-store responses. Bootstrap exposes only the separately revocable paired
client credential, never owner authority. Keep client/Worker proof credentials
in request-local memory, outside URL parameters, browser storage, query caches,
build output, run manifests and logs. No arbitrary path or command HTTP interface
exists. Owned metadata inspection cannot enumerate the user's native store.
Native entries keep their existing server/reference ownership and cleanup rules.

## Logging

Emit structured operation, worker, phase, state and stable error-code fields.
Do not forward child output, raw exceptions, authentication, native content or
user input. Print generated worker URLs and retained artifact locations only in
the explicit run manifest. Record actual validation revision, commands, results
and remaining browser/native/account limits in PRs, issues and CI artifacts.

## Build and Test

`pnpm test` in `apps/delidev` includes QA host/launcher tests. Integration tests
use real temporary Go servers, Workers, SQLite and Git repositories. Browser
acceptance uses host-supplied Playwright/Chrome, with no product dependency.
Check two same-profile browser pages for independent create/edit/delete/reload,
authentication revocation, Worker inspection/control, server Stop/explicit Start,
cross-origin rejection and package exclusion. Cover startup failure, repeated
controls, signals and cleanup uncertainty. Do not equate these checks with CEF,
real AI account or platform installation acceptance.

Run browser acceptance explicitly:

```sh
DELIDEV_QA_PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs \
  pnpm --filter delidev-desktop test:qa:browser
```

The optional `DELIDEV_QA_BROWSER_CHANNEL` selects an installed browser; default
is Chrome. Both browser pages share one browser context and use actual product
RPCs and Workers. `browser-validation.json` and synthetic screenshots remain in
the external artifacts directory. `test:qa` runs the Go/host/lifecycle integration
tests; ordinary `pnpm test` also verifies that the built release frontend excludes
the QA entry and host markers. No validation record is added to this repository.

## Dependencies and Integrations

Reuse the ordinary CLI, Go server/Worker, protected-storage ownership, generated
TypeScript client, existing App and bounded process-tree termination utilities.
QA builds are separate from ordinary desktop/frontend packaging.

## Change Triggers

Update this contract, the desktop contract, project index/catalog and scoped
app/frontend/script AGENTS when QA ownership or authority changes. Changes to
product lifecycle/credential rules also require their original domain owners.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [Project index](project-delidev.md)
- [Desktop client](apps-delidev-desktop-contract.md)
- [Go CLI/server/Worker](cmds-delidev-contract.md)
- [Protected credentials](cmds-delidev-credentials-contract.md)
- [Source ownership and validation](cmds-delidev-structure-contract.md)
- [Repository defaults](repository-defaults.md)

The the feature session-remediation layout fixture exercises the real shared task presentation with synthetic closed startup evidence and original-controller counters. Validate bilingual themes, narrow widths and original draft/confirmation retention; keep its separate entry and browser outputs outside product releases. It cannot prove native startup, credential access, real-account acceptance or packaged CEF behavior.

The the feature account-remediation fixture similarly exercises safe ChatGPT/API account failure presentation, original read rechecks and compact rail failure popovers in localized responsive themes. Its separate marker is excluded from release output; synthetic counters never establish real login, OS authorization or account cleanup.
