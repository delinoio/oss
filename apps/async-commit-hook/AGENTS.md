# async-commit-hook application ownership

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
- Public documentation and installers are owned by the separate async-commit-hook-docs app. The UI links to https://ach.delino.io.
- `build:embedded` is the sole producer of command webassets/dist: clear the previous embed before building, validate real hashed assets and copy only after success. Include license files; never substitute a placeholder bundle.
- Browser tests must use isolated jsdom storage, never Node file-backed Web Storage; retain the test-worker compatibility flag until Vitest overrides native storage consistently.
- Run `pnpm test` from this directory after frontend changes. Generated `dist` is untracked and must be removed from the final worktree.

- Run-list rows use the optional server check count, falling back to checks.length only for older responses; opening a row retrieves complete execution details.

- Poll expanded logs only while their check is active, fetch final bytes once on completion, and retain explicit refresh and pagination for terminal evidence.

- Load repository pages on demand with Connect Query, merge split repositories/worktrees by ID, and retain loaded navigation and selection across next-page errors. Keep the load control focusable during loading and guard repeated activation.

- Load branches on demand, retaining the selected branch even when its page is not loaded; preserve loaded options and selection through next-page errors.

- Key branch options by opaque identity, not normalized display text, and pass branch_id to source/history queries. Keep current identities before their page loads and preserve legacy servers without IDs.

- Checks and Inbox queries require both a selected repository and worktree. Pending, empty or failed discovery must never issue an unfiltered run query or render cached unscoped rows.

- Execution deep links bind the page heading, path, selected worktree and branch navigation to the run's repository/worktree IDs. Suppress unrelated identity while the run or its registry page is pending, failed or unavailable; keep evidence accessible and registry pagination on demand. Share the detail query cache without additional polling or acknowledgement.
