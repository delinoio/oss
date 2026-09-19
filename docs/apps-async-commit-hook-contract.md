# async-commit-hook application contract

## Scope
`apps/async-commit-hook`: static application and `/docs` at https://ach.delino.io.

## Runtime and Language
React/TypeScript, Rsbuild, React Query with Connect Query, Cloudflare Pages. Fixed localhost development port 46308; conflicts and address overrides fail.

## Users and Operators
Developers using current desktop Chrome/Edge; local results are never uploaded to hosting.

## Interfaces and Contracts
The acknowledgement action stays disabled while an execution is queued, preparing, running or collecting; only completed results may be acknowledged.
Selecting Detached HEAD in Checks shows detached executions only. Inbox spans branches in the selected worktree; switching a filter resets its cursor.
Repository/worktree and branch navigation; Changes, Commits, Checks, Inbox; run detail, logs, failures and comparison. Explicit acknowledgement, rerun and cancellation. No configuration authoring or arbitrary commands. Changes use configured base, local origin/HEAD, then explicit selection; compare the merge base without fetching.

Pairing receives a single-use five-minute code through a URL fragment or user entry. Remove the fragment after reading. Persist browser authorization per local installation; show revocation, network permission denial, unpaired/disconnected and version errors. Missing data, empty/loading, queued/running, failed/cancelled/interrupted states have actionable next steps. Keyboard, focus, screen-reader and non-color status behavior are required.

## Storage

When a fresh pairing fragment accompanies stale stored authorization, Pair again removes that authorization and clears query data while retaining the consumed fragment's in-memory code for the pairing form. Successful pairing and explicit disconnect discard the code.

Detail navigation focuses the commit heading, returning to results restores the selected row, and polling never steals focus. Cancellation uses a native modal dialog with Escape and prior-focus restoration. Browsers exposing the local-network-access Permissions API receive a distinct denied-permission recovery message; older browser APIs retain ordinary connection guidance.
Browser authorization and local connection preferences only; result cache is in memory. Durable results belong to the CLI's local state. Public docs cover configuration, installation, CLI/MCP/skills, privacy, validation meaning, compatibility and recovery.

## Security
Strict static CSP; logs/source rendered as text; no remote logging/analytics. Connect only to explicit loopback URLs. No secret in URL query strings. API authorization remains authoritative.

## Logging
No automatic uploads or persistent result logging in the browser. Surface typed local diagnostics.

## Build and Test
Package-local pnpm test, typecheck, component/accessibility tests, production build and route/security tests. Root development entry is pnpm dev:async-commit-hook.

## Dependencies and Integrations
Generated @delinoio/async-commit-hook-api-client; Cloudflare Pages deployment is manual and not executed during this implementation.

## Change Triggers
Update project/protocol/client contracts, app AGENTS and public docs alongside user-visible changes.

## References
- [Project](project-async-commit-hook.md)
- [Repository defaults](repository-defaults.md)

Execution-detail queries poll every 1.5 seconds only while queued, preparing, running or collecting. Polling stops after a terminal response so integrity verification does not continuously rehash immutable evidence. Explicit query invalidation, mutations and navigation can still refresh completed data.
