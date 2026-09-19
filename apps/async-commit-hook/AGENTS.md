# async-commit-hook application ownership

- The package owns development on fixed port 46308, tests and static production builds. The root entry delegates to this package.
- Development shutdown uses the shared process-tree owner and awaits POSIX group or Windows taskkill cleanup before exiting; terminating only the package-manager parent is insufficient.
- Include the shared process resolver and termination helper in the app test's Turbo inputs and CI path selection.
- Use the generated Connect Query descriptors for server state. Filter lists on the server before applying their scoped cursors.
- The detached branch selection sends an explicit detached filter; the inbox leaves branches unfiltered.
- Disable acknowledgement until an execution is terminal; opening or waiting for a result never acknowledges it.
- Poll execution details only while active; terminal evidence verification must not repeat on a timer. Explicit refresh and mutation invalidation remain available.
- Render source, logs, report data and failure diagnostics as inert text. Do not use HTML interpretation or remote telemetry.
- Pairing codes are short-lived URL fragments or explicit user input. Remove fragments immediately; keep results only in in-memory query caches.
- Authentication recovery clears stale authorization and cached results but retains an unconsumed in-memory pairing code; explicit disconnect still discards it.
- Preserve keyboard navigation, dialog focus restoration, status words and recoverable network/version/authentication states.
- Public documentation and installers live under `public`; internal implementation contracts remain in `docs/`.
- Keep shell installer `--version` behavior aligned with the public guide, validate all arguments before downloads, and preserve signature/checksum verification for every selected version.
- Public recovery guidance must distinguish confirmed descendant cleanup from lost ownership proof: incomplete cancellation blocks replacement, and a forcibly killed Linux supervisor requires host-reboot recovery. Do not claim actual minimum-OS machine qualification from local tests or cross-builds.
- Run `pnpm test` from this directory after frontend changes. Generated `dist` is untracked and must be removed from the final worktree.
