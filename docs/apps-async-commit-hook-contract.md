# async-commit-hook application contract

## Scope
`apps/async-commit-hook`: local UI embedded in the `ach` executable. Public documentation is owned separately by `apps/async-commit-hook-docs` at https://ach.delino.io.

## Runtime and Language
React/TypeScript, Rsbuild, React Query with Connect Query. The Go daemon and on-demand viewer serve the same compiled UI and Connect API on the configured loopback endpoint. No Node server or external assets are required at runtime. Fixed localhost development port 46308; conflicts and address overrides fail.
The package development wrapper uses the shared command resolver and `spawnDevServer` with process-tree termination enabled. SIGINT/SIGTERM wait for the POSIX process group or Windows `taskkill /t` cleanup before wrapper exit, including package-manager and command-wrapper descendants. Script integration fixtures verify immediate port reuse after both signals.
The shared resolver and process-tree helper are explicit Turbo test inputs and CI selection paths, so their changes invalidate application test evidence.

## Users and Operators
Developers using current desktop Chrome/Edge; local results are never uploaded to hosting.

## Interfaces and Contracts
The acknowledgement action stays disabled while an execution is queued, preparing, running or collecting; only completed results may be acknowledged.
Selecting Detached HEAD in Checks shows detached executions only. Inbox spans branches in the selected worktree; switching a filter resets its cursor.
Repository/worktree and branch navigation; Changes, Commits, Checks, Inbox; run detail, logs, failures and comparison. Explicit acknowledgement, rerun and cancellation. No configuration authoring or arbitrary commands. Changes use configured base, local origin/HEAD, then explicit selection; compare the merge base without fetching.

Open the local URL printed by `ach ui` without pairing, login or browser authorization. The Connect transport uses `window.location.origin` with mandatory `X-Ach-Api-Version: 1`; it never accepts a renderer-selected API authority. Keep `#run=ID` for refreshable execution links and discard retired pairing/port fragment fields without reading stored browser tokens. Disconnected, version, empty/loading and execution states retain actionable recovery and accessible keyboard/focus behavior. Documentation opens https://ach.delino.io separately.

A rerun accepted before a startup failure shows its run ID, diagnostic, recovery hint, status command and an Open accepted execution action. Both rerun submission buttons stay disabled after acceptance to avoid accidental duplicate attempts. Successful startup navigates directly to the accepted execution.

## Storage

Detail navigation focuses the commit heading, returning to results restores the selected row, and polling never steals focus. Cancellation uses a native modal dialog with Escape and prior-focus restoration. No browser credentials or connection preferences are persisted; result cache is in memory. Durable results belong to local CLI state. Recovery tells users to run `ach ui` and reload, retaining the on-demand viewer while needed.

## Security
Strict CSP with same-origin scripts, styles and connections; logs/source rendered as text; no remote logging/analytics. Bind only 127.0.0.1 and validate the configured Host. Every RPC requires exact same-origin POST and a single API version header; missing/null/foreign origins are denied without CORS grants. The development proxy validates localhost/127.0.0.1:46308 Host, matching Origin, POST and the version header before rewriting to 127.0.0.1:46309. Local processes are trusted; there is no per-browser identity boundary.

## Logging
No automatic uploads or persistent result logging in the browser. Surface typed local diagnostics.

## Build and Test
Package-local pnpm test, typecheck, component/accessibility tests, production build and route/security tests. Root development entry is pnpm dev:async-commit-hook. `build:embedded` clears the prior embed first, builds the generated client and UI, validates the complete hashed asset inventory, and copies it into the ignored command-owned webassets/dist. It is the sole producer of that embed. Go compilation requires this task first. HTML uses no-store; hashed JavaScript/CSS and their license files are immutable. Only real bundled files are served; missing paths never become an HTML fallback.

Vitest workers disable Node Web Storage so component tests use jsdom-isolated memory. No Node storage file is configured; remove this compatibility flag when Vitest reliably overrides native storage globals.

## Dependencies and Integrations
Generated @delinoio/async-commit-hook-api-client; Go embed ties the UI to the installed executable version. Cloudflare Pages receives only the separate documentation build.

## Change Triggers
Update project/protocol/client contracts, app AGENTS and public docs alongside user-visible changes.

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
