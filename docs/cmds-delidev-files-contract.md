# DeliDev Session Files

## Scope

`cmds/delidev-cli/internal/{domain,workspace,worker,server,cli}` owns read-only session workspace browsing. `apps/delidev` presents the same product operations in the session's right application area. This contract implements the file explorer requirement of issue #964; terminal, diff, browser, editing and downloads remain separate capabilities.

## Runtime and Language

Go 1.25.8 owns validation, filesystem access and authenticated Connect RPC. React uses generated Connect Query bindings.

## Users and Operators

Owners and paired clients inspect the actual execution machine's prepared General Chat, Local or Worktree files, including every repository of a project, while an agent is running.

## Interfaces and Contracts

`SessionService.ReadSessionWorkspace` accepts a session UUID and a closed JSON query: `roots`, `directory`, or `file`. Roots use repository UUIDs; the projectless root has an empty repository UUID. Paths are bounded to 4,096 UTF-8 bytes and 64 components, with at most 255 bytes per component. Paths are canonical relative slash paths (`.` denotes the root), never client-selected absolute directories. Results contain root descriptors, directory entries or an inert UTF-8 text preview. CLI commands are `session files roots|list|read --id ID`, with `--repository-id`, `--path`, and directory `--page-token` as appropriate.

`WorkerService.WatchWorkspaceReads` and `ReportWorkspaceRead` form a separate outbound-only, authenticated observation channel. It is bound to the current primary Worker stream and instance, and cannot renew that stream's lease or authorize execution. At most 1,024 observation streams and 64 pending reads exist per server. One observation per machine is outstanding; a concurrent query receives ResourceExhausted. Reads expire after 15 seconds, never enter the durable execution queue, and cannot block native execution controls. Disconnect, replacement and revocation invalidate pending observations. The server revalidates current session/preparation and Worker ownership before releasing a result.

Directory pages contain at most 100 entries. Enumeration is bounded at 10,000 entries and fails explicitly beyond that limit. Page tokens bind the session, repository, path and digest of the sorted directory observation; changes require restarting pagination. Filenames unsupported by the portable path contract cause an explicit Unsupported result rather than disappearing. Symbolic links and special files are displayed as inert entries, never opened as previews. Text previews read at most 64 KiB plus a sentinel; binary or invalid UTF-8 data has no text preview. Truncated text is labeled; live filesystem observations are not atomic snapshots.

## Storage

Original accepted preparation and Worker manifests select roots. File contents and observation requests/results remain in memory; no database, event, receipt, transcript, search, disk cache or synced setting retains them. Client query caches have no persistence and are removed when the explorer closes or changes session. Navigation preserves the session and unsent composer input.

## Security

The server never opens Worker paths. The Worker compares its original manifest and validates current workspace/Git identity independently of native execution locks, using a separate process owner for read-only Git checks. Go `os.Root` anchors descendant access. Paths reject traversal, absolute paths, backslashes, control characters, Windows-reserved punctuation/alternate streams/device names and ambiguous trailing dots/spaces. Links cannot supply a root or a file preview; internal directory links are not navigation targets. Opened files must be regular and bounded; Unix nonblocking open prevents a swapped FIFO from hanging the reader. Root identity is checked around observation. These anchors do not sandbox privileged bind mounts or make live files immutable. No tool transcript path, citation or model-generated locator grants file access. Reads do not fetch, checkout, repair, execute, reset or alter workspace claims.

## Logging

Structured logs contain observation/session/machine UUIDs, operation and sanitized closed error code only. Never log filenames, paths, contents, native error messages or credentials.

## Build and Test

Run focused domain/workspace/server/Worker/CLI tests, the complete DeliDev Go suite and vet. Run `pnpm test` in `apps/delidev` for frontend changes. Verify generation with the repository Buf configuration. Test active-execution independence, scoped ownership, disconnect/revocation, malformed/late responses, root/path/symlink confinement, binary/truncated previews, pagination changes and composer preservation. Record actual native platform evidence separately from unit/integration fixtures.

## Dependencies and Integrations

Uses the existing preparation manifest, authenticated Connect services, Worker process lifecycle and generated API client. No new network listener, Tauri business binding or filesystem dependency is introduced.

## Change Triggers

Update the project index, protocol/client/desktop contracts and relevant scoped AGENTS when authority, bounds, paths or presentation change.

## References

- [Project](project-delidev.md)
- [Workspace](cmds-delidev-workspace-contract.md)
- [Requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Go traversal-resistant filesystem APIs](https://go.dev/blog/osroot)
