# Get started

```
cd your-repository
ach init
# Edit .config/async-commit-hook.toml, then commit it.
git add .config/async-commit-hook.toml
git commit -m "Configure local checks"
ach inbox --repo .
ach ui
```

`init` explicitly trusts this local repository and its subsequent committed command changes. Linked worktrees register separately under the same repository. Separate clones are never merged based on remote URL. If a hook already exists, ach prints a manual integration command instead of overwriting it.

The post-commit receipt identifies the exact commit and run. It includes status/wait commands, MCP guidance and a browser URL (or `ach ui --run ID` in on-demand mode). Git can return before checks finish. A startup error may leave a saved request pending; a failed save never produces a false queued receipt. If a browser rerun is accepted but cannot start, the page retains its execution ID and shows recovery guidance with an Open accepted execution button. Inspect that saved attempt instead of submitting another rerun.

Execution links select the run's repository and worktree. If its workspace is on a later page, use **Load more workspaces** to restore workspace navigation. Its retained execution evidence remains readable while those workspace details are unavailable.
