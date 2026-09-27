# DeliDev Session Files and Git Comparisons

## Scope

`cmds/delidev-cli/internal/{domain,workspace,worker,server,cli}` owns read-only session workspace browsing and Git comparisons. `apps/delidev` presents the same product operations in the session's right application area. This contract implements file browsing and bounded Git comparisons for issue #964; local review comments/submissions, terminal, browser, editing and downloads remain separate capabilities.

## Runtime and Language

Go 1.25.8 owns validation, filesystem access and authenticated Connect RPC. React uses generated Connect Query bindings.

## Users and Operators

Owners and paired clients inspect the actual execution machine's prepared General Chat, Local or Worktree files, including every repository of a project, while an agent is running.

## Interfaces and Contracts

`SessionService.ReadSessionWorkspace` accepts a session UUID and a closed JSON query: `roots`, `directory`, `file`, or `git-diff`. Roots use repository UUIDs; the projectless root has an empty repository UUID. Paths are bounded to 4,096 UTF-8 bytes and 64 components, with at most 255 bytes per component. Paths are canonical relative slash paths (`.` denotes the root), never client-selected absolute directories. Results contain root descriptors, directory entries or an inert UTF-8 text preview. CLI commands are `session files roots|list|read --id ID`, with `--repository-id`, `--path`, and directory `--page-token` as appropriate.

`WorkerService.WatchWorkspaceReads` and `ReportWorkspaceRead` form a separate outbound-only, authenticated observation channel. It is bound to the current primary Worker stream and instance, and cannot renew that stream's lease or authorize execution. At most 1,024 observation streams and 64 pending reads exist per server. One observation per machine is outstanding; a concurrent query receives ResourceExhausted. Reads expire after 15 seconds, never enter the durable execution queue, and cannot block native execution controls. Disconnect, replacement and revocation invalidate pending observations. The server revalidates current session/preparation and Worker ownership before releasing a result.

Directory pages contain at most 100 entries. Enumeration is bounded at 10,000 entries and fails explicitly beyond that limit. Page tokens bind the session, repository, path and digest of the sorted directory observation; changes require restarting pagination. Filenames unsupported by the portable path contract cause an explicit Unsupported result rather than disappearing. Symbolic links and special files are displayed as inert entries, never opened as previews. Text previews read at most 64 KiB plus a sentinel; binary or invalid UTF-8 data has no text preview. Truncated text is labeled; live filesystem observations are not atomic snapshots.

## Git Comparisons

`session diff --id ID --repository-id ID [--comparison working-tree|staged|creation] [--path RELATIVE]` uses the same owner/client `ReadSessionWorkspace` and outbound Worker observation channel. The equivalent closed JSON operation is `git-diff`, with explicit `comparison`, repository UUID and canonical relative path. The default CLI comparison is `working-tree`. Desktop defaults to `creation` for Worktree sessions and `working-tree` for Local. General Chat without a prepared repository is unavailable, rather than interpreting an incidental nested Git directory as a configured repository.

- `working-tree` compares tracked working files, including staged changes, against the current exact HEAD.
- `staged` compares the Git index against the current exact HEAD.
- `creation` compares tracked working files against that repository's immutable resolved Worktree creation commit. It is unavailable for Local; no session-start filesystem baseline is created.

An independently verified unborn Local branch compares against Git's canonical empty tree without writing an object, branch or commit. The result retains the comparison, repository, relative path, base kind/object, optional actual HEAD, complete patch, separately listed untracked paths and a SHA-256 revision over those exact returned facts. HEAD changes during the operation reject the observation. Live working-tree/index reads are not atomic filesystem snapshots; the revision identifies returned bytes and grants no patch application, local review or execution authority.

Git runs with an independent read process owner and original manifest/administrative identity checks before and after the observation. Native execution claims remain untouched. Literal pathspecs prevent option/glob expansion. Diff and text-conversion helpers are disabled, and every read-only inspection child disables configured filesystem monitors, including index-opening attribute checks. Active clean/process filter attributes on selected tracked paths block a working-tree comparison; unused global driver definitions do not. Staged comparisons read stored Git content. Repository configuration/attributes and files remain live user-owned inputs, not a new OS sandbox.

The patch is limited to 64 KiB without truncation; larger comparisons return ResourceExhausted with narrower-path guidance. Binary changes keep Git's binary indication without fabricated lines. Submodules show Gitlink commit differences only; nested dirty/untracked content is explicitly outside this comparison. At most 100 untracked paths / 16 KiB of names are listed separately and never represented as included patch hunks. Filter inspection is bounded by 10,000 tracked paths. Invalid UTF-8, NUL text, unsupported portable paths and incomplete/mixed observations fail rather than disappearing. No fetch, checkout, staging, commit, filesystem baseline or shared-checkout mutation is performed.

Desktop Files and Diff share the existing right session area while preserving the conversation and unsent composer. Diff supports repository/comparison selection, a literal relative path, explicit refresh, keyboard Escape and focus return. Patch text and filenames remain inert. Failed refresh retains and labels the prior observation; closing or changing the view cancels outstanding reads and discards inactive query contents. No background polling or persistent cache is added.

## Local Review Coordinates

`SessionService.ReadSessionReviewContext` and `session review-context --id ID --repository-id ID [--comparison working-tree|staged|creation] [--path RELATIVE]` return the exact Worker Git observation plus validated ordinary unified-diff files and line coordinates. They share the original owner/client authorization, prepared workspace, outbound Worker, deadline and bounded read process. They do not accept caller-provided patch bytes. Read-only Git children pin `LC_ALL=C` so binary and final-newline markers have one grammar regardless of the Worker locale. Structured output is limited to 1 MiB without partial results; at most 256 changed files are interpreted.

The parser requires matching no-rename file identities, portable paths within the original query, supported modes, exact old/new headers and complete non-overlapping hunk counts. Git-quoted UTF-8 and unquoted filenames containing spaces preserve their original paths. Each ordinary text line retains its old/new number (absence is zero), original text, final-newline presence and hunk identity. Combined/unknown/incomplete/duplicate/ambiguous forms fail explicitly. Binary, mode-only, empty-file, symbolic-link and submodule changes expose file locations only, never fabricated line anchors.

A review selection identifies one file or up to 20 visible consecutive lines on one side in one hunk. The domain anchor binds repository, comparison, query path, exact diff revision, selected file/side/range, SHA-256 of the original file patch and at most 8 KiB of exact selected context. Validation never silently relocates a comment: a changed comparison or context does not match the retained anchor. This read-only increment establishes coordinate evidence; durable comment CRUD, freshness presentation and atomic grouped agent submissions remain separate required work.

## Storage

Original accepted preparation and Worker manifests select roots. File contents and observation requests/results remain in memory; no database, event, receipt, transcript, search, disk cache or synced setting retains them. Client query caches have no persistence and are removed when the explorer closes or changes session. Navigation preserves the session and unsent composer input.

## Security

The server never opens Worker paths. The Worker compares its original manifest and validates current workspace/Git identity independently of native execution locks, using a separate process owner for read-only Git checks. Go `os.Root` anchors descendant access. Paths reject traversal, absolute paths, backslashes, control characters, Windows-reserved punctuation/alternate streams/device names and ambiguous trailing dots/spaces. Links cannot supply a root or a file preview; internal directory links are not navigation targets. Opened files must be regular and bounded; Unix nonblocking open prevents a swapped FIFO from hanging the reader. Root identity is checked around observation. These anchors do not sandbox privileged bind mounts or make live files immutable. No tool transcript path, citation or model-generated locator grants file access. Reads do not fetch, checkout, repair, execute, reset or alter workspace claims.

## Logging

Structured logs contain observation/session/machine UUIDs, operation and sanitized closed error code only. Never log filenames, paths, contents, native error messages or credentials.

## Build and Test

Run focused domain/workspace/server/Worker/CLI tests, the complete DeliDev Go suite and vet. Run `pnpm test` in `apps/delidev` for frontend changes. Verify generation with the repository Buf configuration. Test active-execution independence, scoped ownership, disconnect/revocation, malformed/late responses, root/path/symlink confinement, binary/truncated previews, pagination changes and composer preservation. Record actual native platform evidence separately from unit/integration fixtures.

## Dependencies and Integrations

Uses native Git comparisons, the existing preparation manifest, authenticated Connect services, Worker process lifecycle and generated API client. No new network listener, Tauri business binding or filesystem dependency is introduced.

## Change Triggers

Update the project index, protocol/client/desktop contracts and relevant scoped AGENTS when authority, bounds, paths or presentation change.

## References

- [Project](project-delidev.md)
- [Workspace](cmds-delidev-workspace-contract.md)
- [Requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Go traversal-resistant filesystem APIs](https://go.dev/blog/osroot)
- [Git diff](https://git-scm.com/docs/git-diff)
- [Git attributes and filters](https://git-scm.com/docs/gitattributes)

- [Git patch format](https://git-scm.com/docs/diff-format)
