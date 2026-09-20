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
- Pairing codes are short-lived URL fragments or explicit user input. Remove fragments immediately; keep results only in in-memory query caches.
- Authentication recovery clears stale authorization and cached results but retains an unconsumed in-memory pairing code; explicit disconnect still discards it.
- Preserve keyboard navigation, dialog focus restoration, status words and recoverable network/version/authentication states.
- Public documentation and installers live under `public`; internal implementation contracts remain in `docs/`.
- Keep shell installer `--version` behavior aligned with the public guide, validate all arguments before downloads, and preserve signature/checksum verification for every selected version.
- Primary public install commands use the downloaded installers' synchronized defaults, and the primary self-update command selects the highest published stable version. Keep explicit version-selection and rollback guidance separate so published commands never pin an old product release.
- Public recovery guidance must distinguish confirmed descendant cleanup from lost ownership proof: incomplete cancellation blocks replacement, and a forcibly killed Linux supervisor requires host-reboot recovery. Do not claim actual minimum-OS machine qualification from local tests or cross-builds.
- Run `pnpm test` from this directory after frontend changes. Generated `dist` is untracked and must be removed from the final worktree.

- Run-list rows use the optional server check count, falling back to checks.length only for older responses; opening a row retrieves complete execution details.

- Poll expanded logs only while their check is active, fetch final bytes once on completion, and retain explicit refresh and pagination for terminal evidence.

- Load repository pages on demand with Connect Query, merge split repositories/worktrees by ID, and retain loaded navigation and selection across next-page errors. Keep the load control focusable during loading and guard repeated activation.

- Load branches on demand, retaining the selected branch even when its page is not loaded; preserve loaded options and selection through next-page errors.

- Key branch options by opaque identity, not normalized display text, and pass branch_id to source/history queries. Keep current identities before their page loads and preserve legacy servers without IDs.

- Checks and Inbox queries require both a selected repository and worktree. Pending, empty or failed discovery must never issue an unfiltered run query or render cached unscoped rows.

- Execution deep links bind the page heading, path, selected worktree and branch navigation to the run's repository/worktree IDs. Suppress unrelated identity while the run or its registry page is pending, failed or unavailable; keep evidence accessible and registry pagination on demand. Share the detail query cache without additional polling or acknowledgement.
