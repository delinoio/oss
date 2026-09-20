# Browser connection

`ach ui` prints the local web UI URL after the server is ready. Open it in desktop Chrome or Edge; no pairing code or browser authorization is needed. The default URL is `http://127.0.0.1:46309/`. Your personal `api_port` setting applies to both the UI and API.

In daemon mode, ach serves the UI and API until the daemon stops. In on-demand mode, `ach ui` starts a temporary viewer until Ctrl-C; stopping it does not stop checks or remove results. Use `ach ui --run <id>` to open a specific execution. The UI is included in the executable, so viewing local results does not require Node or internet access.

The server accepts only its exact loopback address and same-origin browser RPC requests. Remote API hosts are unsupported. Local processes on your computer are trusted; browser access is no longer separated by per-browser credentials. `ach browser list` and `ach browser revoke` are retired and return guidance to use `ach ui`.

If the server stops, run `ach ui` again and reload its URL. After upgrading ach, reload the page to load the matching UI. Change `api_port` only while ach is stopped; port conflicts fail without choosing another port.

The UI supports repositories/worktrees, branches, Changes, Commits, Checks, Inbox, logs, structured failures, comparison, acknowledgement, rerun and cancellation. Commands/configuration are authored locally. Changes prefer the configured diff base, then locally known origin/HEAD, otherwise require selection, and show the merge base without fetching.
