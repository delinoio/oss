# async-commit-hook application contract

## Scope
`apps/async-commit-hook`: local UI embedded in the `ach` executable. Public documentation is owned by `apps/public-docs/docs/async-commit-hook` at https://oss.delino.io/async-commit-hook.

## Runtime and Language
React/TypeScript, Rsbuild, React Query with Connect Query. The Go daemon and on-demand viewer serve the same compiled UI and Connect API on the configured loopback endpoint. No Node server or external assets are required at runtime. Fixed localhost development port 46308; conflicts and address overrides fail.
The package development wrapper uses the shared command resolver and `spawnDevServer` with process-tree termination enabled. SIGINT/SIGTERM wait for the POSIX process group or Windows `taskkill /t` cleanup before wrapper exit, including package-manager and command-wrapper descendants. Script integration fixtures verify immediate port reuse after both signals.
The shared resolver and process-tree helper are explicit Turbo test inputs and CI selection paths, so their changes invalidate application test evidence.

## Users and Operators
Developers using current desktop Chrome/Edge; local results are never uploaded to hosting.

## Interfaces and Contracts
The acknowledgement action stays disabled while an execution is queued, preparing, running or collecting; only completed results may be acknowledged.
Selecting Detached HEAD in Checks shows detached executions only. Inbox spans branches in the selected worktree; switching a filter resets its cursor.
Repository/worktree and branch navigation; Changes, Commits, Checks, Inbox; run detail, logs, failures and comparison. Explicit acknowledgement, rerun and cancellation. No configuration authoring or arbitrary commands. Changes use configured base, local origin/HEAD, then explicit selection; compare the merge base without fetching. When no automatic base is available, the `diff-base-required` diagnostic directs the user to choose a local base and is never rendered as a connection outage.

Open the local URL printed by `ach ui` without pairing, login or browser authorization. The Connect transport uses `window.location.origin` with mandatory `X-Ach-Api-Version: 1`; it never accepts a renderer-selected API authority. Keep `#run=ID` for refreshable execution links and discard retired pairing/port fragment fields without reading stored browser tokens. Disconnected, version, empty/loading and execution states retain actionable recovery and accessible keyboard/focus behavior. Documentation opens https://oss.delino.io/async-commit-hook separately.

A rerun accepted before a startup failure shows its run ID, diagnostic, recovery hint, status command and an Open accepted execution action. Both rerun submission buttons stay disabled after acceptance to avoid accidental duplicate attempts. Successful startup navigates directly to the accepted execution.

### Application integration

- `apps/public-docs`: Rspress static public documentation app, including the `docs/binpm`, `docs/nodeup`, `docs/runmoor`, `docs/async-commit-hook`, `docs/clibox`, `docs/pnport`, and `docs/react-forge` content roots.

- `public-docs` is the sole production documentation publisher. It builds the `docs/runmoor`, `docs/nodeup`, `docs/binpm`, `docs/async-commit-hook`, `docs/clibox`, `docs/pnport`, and `docs/react-forge` content roots directly below their same-name canonical subpaths; no package-local documentation workspaces or output directories are independently published.

- Every assembled documentation page must expose the shared site selector for Delino OSS, Runmoor, Nodeup, binpm, async-commit-hook, clibox, pnport, and React Forge. Production and the consolidated development server at port `46302` use the same clean relative destinations; never remap project links to retired per-project ports. It must expose `aria-expanded` and `aria-current`, support keyboard selection, Escape/outside-click close, and focus return.

- Nodeup, binpm, Runmoor, async-commit-hook, clibox, pnport, and React Forge are exposed from `apps/public-docs/docs` through canonical same-origin subpaths `/nodeup`, `/binpm`, `/runmoor`, `/async-commit-hook`, `/clibox`, `/pnport`, and `/react-forge`. Their Markdown is owned directly by these content roots and must not be duplicated elsewhere. pnport's guides describe verified 0.1.2 availability with known macOS failures and incomplete full acceptance; future availability changes require verified distribution.

- Public-docs `build`, `build:frontend`, and `ci:routes` must hash all eight canonical `scripts/install/{nodeup,binpm,async-commit-hook,pnport}.{sh,ps1}` sources through package-local external inputs. Preserve inherited build inputs, dependencies, outputs, and deterministic caching; installer changes must not invalidate unrelated workspace caches.

- `apps/async-commit-hook` owns the embedded local Rsbuild UI. `apps/public-docs/docs/async-commit-hook` owns the Rspress documentation content at `https://oss.delino.io/async-commit-hook`, and `scripts/install` owns the installer sources. Follow the project contracts.

- Fixed development port 46308; root entry `pnpm dev:async-commit-hook`. Use generated Connect Query and React Query. Logs/source remain inert, every RPC is same-origin without browser pairing, and results never go to static hosting.

- The local UI app exclusively generates and validates command webassets/dist via `build:embedded`. Generate before Go compilation, tests or packaging.

- Documentation development uses the consolidated `127.0.0.1:46302` public-docs server. Preserve all migrated guide sections, the `/docs` section-link migration, and installer bytes. The async release workflow validates the consolidated docs but does not deploy a standalone site.

- The canonical public-docs production origin is `https://oss.delino.io`; shared Linux package guidance is `https://oss.delino.io/linux-packages`. Consolidated project documentation uses the same origin with `/runmoor`, `/nodeup`, `/binpm`, `/async-commit-hook`, `/clibox`, `/pnport`, and `/react-forge` prefixes.

- Public-docs cached `build`, `build:frontend`, and `ci:routes` tasks must hash the eight canonical `scripts/install/{nodeup,binpm,async-commit-hook,pnport}.{sh,ps1}` sources. Preserve inherited build inputs, generated outputs, and exact installer byte checks; CI job selection alone does not invalidate task caches.

### apps/async-commit-hook constraints

- The package owns development on fixed port 46308, tests and static production builds. The root entry delegates to this package.

- Development shutdown uses the shared process-tree owner and awaits POSIX group or Windows taskkill cleanup before exiting; terminating only the package-manager parent is insufficient.

- Include the shared process resolver and termination helper in the app test's Turbo inputs and CI path selection.

- Use the generated Connect Query descriptors for server state. Filter lists on the server before applying their scoped cursors.

- The detached branch selection sends an explicit detached filter; the inbox leaves branches unfiltered.

- Preserve accepted rerun IDs when startup fails: show the startup diagnostic, recovery hint and a direct execution action; disable repeated submission after acceptance.

- Disable acknowledgement until an execution is terminal; opening or waiting for a result never acknowledges it.

- Poll execution details only while active; terminal evidence verification must not repeat on a timer. Explicit refresh and mutation invalidation remain available.

- Render source, logs, report data and failure diagnostics as inert text. Do not use HTML interpretation or remote telemetry.

- The daemon and on-demand viewer serve the same embedded UI. Use same-origin Connect with mandatory API version headers, no pairing or browser credential persistence; keep result caches in memory and run fragments refreshable.

- Reject foreign/missing Origin, Host, method and version header before the development proxy rewrites a request. Production API security cannot depend on a dev-only CORS grant.

- Preserve keyboard navigation, dialog focus restoration, status words and recoverable network/version states.

- Public documentation is owned by `apps/public-docs/docs/async-commit-hook`, and installers are maintained in `scripts/install`. The UI links to https://oss.delino.io/async-commit-hook.

- `build:embedded` is the sole producer of command webassets/dist: clear the previous embed before building, validate real hashed assets and copy only after success. Include license files; never substitute a placeholder bundle.

- Run-list rows use the optional server check count, falling back to checks.length only for older responses; opening a row retrieves complete execution details.

- Poll expanded logs only while their check is active, fetch final bytes once on completion, and retain explicit refresh and pagination for terminal evidence.

- Load repository pages on demand with Connect Query, merge split repositories/worktrees by ID, and retain loaded navigation and selection across next-page errors. Keep the load control focusable during loading and guard repeated activation.

- Load branches on demand, retaining the selected branch even when its page is not loaded; preserve loaded options and selection through next-page errors.

- Key branch options by opaque identity, not normalized display text, and pass branch_id to source/history queries. Keep current identities before their page loads and preserve legacy servers without IDs.

- Checks and Inbox queries require both a selected repository and worktree. Pending, empty or failed discovery must never issue an unfiltered run query or render cached unscoped rows.

- Execution deep links bind the page heading, path, selected worktree and branch navigation to the run's repository/worktree IDs. Suppress unrelated identity while the run or its registry page is pending, failed or unavailable; keep evidence accessible and registry pagination on demand. Share the detail query cache without additional polling or acknowledgement.

## Storage

Detail navigation focuses the commit heading, returning to results restores the selected row, and polling never steals focus. Cancellation uses a native modal dialog with Escape and prior-focus restoration. No browser credentials or connection preferences are persisted; result cache is in memory. Durable results belong to local CLI state. Recovery tells users to run `ach ui` and reload, retaining the on-demand viewer while needed.

## Security
Strict CSP with same-origin scripts, styles and connections; logs/source rendered as text; no remote logging/analytics. Bind only 127.0.0.1 and validate the configured Host. Every RPC requires exact same-origin POST and a single API version header; missing/null/foreign origins are denied without CORS grants. The development proxy validates localhost/127.0.0.1:46308 Host, matching Origin, POST and the version header before rewriting to 127.0.0.1:46309. Local processes are trusted; there is no per-browser identity boundary.

## Logging
No automatic uploads or persistent result logging in the browser. Surface typed local diagnostics.

## Build and Test
Package-local pnpm test, typecheck, component/accessibility tests, production build and route/security tests. Root development entry is pnpm dev:async-commit-hook. `build:embedded` clears the prior embed first, builds the generated client and UI, validates the complete hashed asset inventory, and copies it into the ignored command-owned webassets/dist. It is the sole producer of that embed. Go compilation requires this task first. HTML uses no-store; hashed JavaScript/CSS and their license files are immutable. Only real bundled files are served; missing paths never become an HTML fallback.

## Dependencies and Integrations
Generated @delinoio/async-commit-hook-api-client; Go embed ties the UI to the installed executable version. Cloudflare Pages receives only the separate documentation build.

## Change Triggers
Update project/protocol/client contracts, app AGENTS and public docs alongside user-visible changes.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References
- [Project](project-async-commit-hook.md)
- [Repository defaults](repository-defaults.md)

Execution-detail queries poll every 1.5 seconds only while queued, preparing, running or collecting. Polling stops after a terminal response so integrity verification does not continuously rehash immutable evidence. Explicit query invalidation, mutations and navigation can still refresh completed data.

Checks and Inbox rows display the server-provided total check count without requiring per-check list payloads. Fall back to the check-array length only when an older response omits that count. Row selection still loads complete execution details.

Expanded check logs poll only while that check is active, independently of other checks in the run. A transition to a terminal state triggers one final read; terminal logs support explicit refresh and page navigation without periodic integrity reads.

The repository sidebar loads bounded worktree pages on demand through Connect Query. Load more workspaces preserves the current selection and merges repositories spanning pages by ID. Pending and failed page loads retain loaded history, with a retry action; keyboard activation and focus remain on the load control while more pages exist. Busy and last-page controls use aria-disabled and guarded activation without removing keyboard focus.

The branch selector loads bounded pages on demand through Connect Query, deduplicates names and retains the selected branch before its page loads. Page errors keep prior options and selection available with an explicit retry; loading controls retain focus.

Branch navigation keys options by the opaque server identity, including the current branch before its page loads. Normalized labels are display-only; Changes, Commits and Checks send branch_id, while Inbox remains branch-unfiltered. Older servers without identities retain their legacy string selection.

Checks and Inbox stay inactive until a repository/worktree pair is selected. Pending, empty and failed discovery show selection guidance and never request or display an unfiltered history page; explicit run deep links remain independently addressable.

An execution deep link selects its exact repository/worktree pair from GetRun, including when that worktree is not the first registry entry. Until the run and its matching registry page are available, the main heading/path/branch selector and sidebar selection never identify another workspace. Retained evidence remains independently readable; additional repository pages are still loaded explicitly, and loading the matching page restores workspace navigation. The context lookup shares the detail query cache and does not add polling or acknowledgement. Leaving a resolved detail returns to that worktree's scoped history.
