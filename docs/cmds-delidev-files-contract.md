# DeliDev Session Files and Git Comparisons

## Scope

`cmds/delidev-cli/internal/{domain,workspace,worker,server,cli}` owns read-only session workspace browsing, Git comparisons and durable local reviews. `apps/delidev` presents the same product operations in the session's right application area. This contract implements file browsing, bounded Git comparisons and durable local review comments/submissions for issue #964; terminal, browser, file editing and downloads remain separate capabilities.

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

Git runs with an independent read process owner and original manifest/administrative identity checks before and after the observation. Native execution claims remain untouched. A separate per-session cross-process gate
fences storage/deletion and preparation/recovery through the full anchored
observation and joined read-process cleanup. Busy storage/read admission returns
conflict without side effects; read-only views remain available during execution.
Version-2 read indexes bind their original session before native launch, and
unknown or unassigned legacy ownership remains recovery-required. Literal pathspecs prevent option/glob expansion. Diff and text-conversion helpers are disabled, and every read-only inspection child disables configured filesystem monitors, including index-opening attribute checks. Active clean/process filter attributes on selected tracked paths block a working-tree comparison; unused global driver definitions do not. A working-tree diff uses a private temporary Git admin with the original index and object store as read-only inputs and a highest-precedence `!filter` attribute for every path. This prevents a clean/process driver from running even if attributes or the index change between the preflight check and diff; a second attribute check rejects a filter that remains active afterward. Staged comparisons read stored Git content. Repository configuration/attributes and files remain live user-owned inputs, not a new OS sandbox.

The patch is limited to 64 KiB without truncation; larger comparisons return ResourceExhausted with narrower-path guidance. Binary changes keep Git's binary indication without fabricated lines. Submodules show Gitlink commit differences only; nested dirty/untracked content is explicitly outside this comparison. At most 100 untracked paths / 16 KiB of names are listed separately and never represented as included patch hunks. Filter inspection is bounded by 10,000 tracked paths. Invalid UTF-8, NUL text, unsupported portable paths and incomplete/mixed observations fail rather than disappearing. No fetch, checkout, staging, commit, filesystem baseline or shared-checkout mutation is performed.

Desktop Files and Diff share the existing right session area while preserving the conversation and unsent composer. Diff supports repository/comparison selection, a literal relative path, explicit refresh, keyboard Escape and focus return. Patch text and filenames remain inert. Failed refresh retains and labels the prior observation; closing or changing the view cancels outstanding reads and discards inactive query contents. No background polling or persistent cache is added.

## Local Review Coordinates

`SessionService.ReadSessionReviewContext` and `session review-context --id ID --repository-id ID [--comparison working-tree|staged|creation] [--path RELATIVE]` return the exact Worker Git observation plus validated ordinary unified-diff files and line coordinates. They share the original owner/client authorization, prepared workspace, outbound Worker, deadline and bounded read process. They do not accept caller-provided patch bytes. Read-only Git children pin `LC_ALL=C` so binary and final-newline markers have one grammar regardless of the Worker locale. Structured output is limited to 1 MiB without partial results; at most 256 changed files are interpreted.

The parser requires matching no-rename file identities, portable paths within the original query, supported modes, exact old/new headers and complete non-overlapping hunk counts. Git-quoted UTF-8 and unquoted filenames containing spaces preserve their original paths. Each ordinary text line retains its old/new number (absence is zero), original text, final-newline presence and hunk identity. Combined/unknown/incomplete/duplicate/ambiguous forms fail explicitly. Binary, mode-only, empty-file, symbolic-link and submodule changes expose file locations only, never fabricated line anchors.

A review selection identifies one file or up to 20 visible consecutive lines on one side in one hunk. The domain anchor binds repository, comparison, query path, exact diff revision, selected file/side/range, SHA-256 of the original file patch and at most 8 KiB of exact selected context. Validation never silently relocates a comment: a changed comparison or context does not match the retained anchor. These coordinates establish original location evidence for the separately authorized durable comment and grouped submission operations below.

## Storage

Original accepted preparation and Worker manifests select roots. File contents and observation requests/results remain in memory; no database, event, receipt, transcript, search, disk cache or synced setting retains them. Client query caches have no persistence and are removed when the explorer closes or changes session. Navigation preserves the session and unsent composer input. The desktop keeps accepted safe directory metadata, per-directory cursors and expansion only for the open session/repository/connection scope. All Files roots, tree pages and previews share a serial observation owner, including cancellation settlement. Collapsing fences late descendant results; explicit parent-first Refresh checks the root and expanded directories. Only a complete authoritative parent range proves that a child disappeared. Back restores accepted tree metadata, scroll and focus without reading and discards preview bytes; closing or replacing the scope discards its metadata as well. These presentation lifetimes do not change Worker observation admission, handle anchoring, 100-entry pages, digest cursors, exact sizes or the 64KiB preview bound.

## Security

The server never opens Worker paths. The Worker compares its original manifest and validates current workspace/Git identity independently of native execution locks, using a separate process owner for read-only Git checks. Go `os.Root` anchors descendant access. Paths reject traversal, absolute paths, backslashes, control characters, Windows-reserved punctuation/alternate streams/device names and ambiguous trailing dots/spaces. Links cannot supply a root or a file preview; internal directory links are not navigation targets. Each path component opens relative to its preceding anchored directory, and its opened identity must match the non-link entry observed before and after opening. Directory enumeration and child metadata use handles to the same directory. A replacement to another internal workspace file or directory rejects the observation rather than showing that entry's content. Opened files must be regular and bounded; Unix nonblocking open prevents a swapped FIFO from hanging the reader. Root identity is checked around observation. These anchors do not sandbox privileged bind mounts or make live files immutable. No tool transcript path, citation or model-generated locator grants file access. Reads do not fetch, checkout, repair, execute, reset or alter workspace claims.

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

## Durable Local Review Mutation Contract

The local-review mutation surface is owner/paired-client-only. A comment creation must independently reread the exact requested Worker comparison, match the author's diff revision and derive its anchor on the server; it cannot accept caller-provided context or patch authority. Recheck original prepared-workspace identity at the database commit boundary. Comment edits preserve their original anchor and increment content revision; deletion removes the retained comment without retracting an already accepted agent input or historical submission.

Version-1 `review` resources are closed comment/submission variants. Comments retain body, anchor, content revision and the last submission link; immutable submission records retain selected original comment content/anchors, their current/stale classification, mode and the accepted queue input UUID. Generic authenticated Resource get/list reads these resources. Mutations bind actor, session, exact input and expected entity revisions to reference-only receipts; replay returns current resources without rereading Worker files or repeating submission. At most 1,000 review records per session and 25 distinct selected comment revisions per submission are accepted; each body is at most 8 KiB and the generated ordinary session input must satisfy its existing 256 KiB bound.

Grouped Request changes rereads each selected comparison, treats any changed original observation as stale without reanchoring, and requires explicit `allow_stale` for known stale comments. Unavailable Worker observations are errors, never fresh. Persist the submission, every selected comment link and the ordinary queued input/session accounting in one transaction. Preserve mode, dispatch/Archive/recovery/native eligibility and existing FIFO behavior. Steering remains a separate explicit operation on the accepted input; no review action automatically steers, resolves comments or publishes to GitHub. Logs/events/receipts carry metadata only. Original comment text/context already copied into a submission and queue remains historical evidence after later comment edits/deletion.


`SessionService.CreateLocalReviewComment`, `EditLocalReviewComment`, `DeleteLocalReviewComment` and `SubmitLocalReview` implement this surface. CLI commands are `session review create|edit|delete|submit|list|get --id SESSION`; edit/delete require `--review-id` and exact `--revision`, creation/submission accept their closed JSON documents through `--input`, and editing accepts a closed `{"body":"..."}` document. List/get use existing Resource pagination/reads scoped to the session.

Creation has a 30-second bound; grouped submissions have a 45-second bound and read at most eight distinct original repository/path/comparison groups. CLI workspace reads allow 20 seconds for response headers, beyond the server observation limit of 15 seconds; review commands use 50-second header/command bounds so valid creation/submission can return before the client deadline. Other CLI commands keep their existing limits. Exact accepted submission retries remain read-only even after a selected comment was deleted and linked receipts were redacted: the immutable submission ID is its original request UUID, and the retained input link is read from that submission after Store validates the original actor/request digest. Removed submission/input evidence cannot authorize another queued input. No SQLite schema migration is required.

The desktop Diff panel authors whole-file or visible old/new-line comments, shows exact original context and current/stale/unchecked comparison state, supports revision-checked edits/deletion, and retains selected record revisions across review pages. Request changes explicitly selects Execute or Plan and optional stale consent. Uncertain mutations retain their exact wire requests across panel navigation; reloading current records does not replace a pending selection or unsent edit. Submitted snapshots remain independently inspectable after later comment edits/deletion. This implements acceptance into the ordinary agent queue; it does not establish unsupported native continuation or actual hosted-model response evidence.

Pending desktop deletion status and explicit retry belong to the displayed Session independently of the deleted comment row or review page. Preserve them through refresh, pagination and panel reopening using the connection-scoped registry. Retry sends only the original mutation, including its exact expected revision and request UUID; clear it only when the returned comment ID and request UUID both match. A missing current comment cannot settle the request, recreate its content or change historical submissions. Foreign Session intents remain isolated, and navigation cannot trigger automatic replay.

`TestManualNativeCLILocalReview` is an explicit installed-Codex acceptance fixture using `DELIDEV_NATIVE_THREAD_EXECUTABLE`. It uses public CLI setup and real protocol discovery, two private Worktrees, independently read original line comments, grouped ordinary input, preserved native history and one native command that updates both selected files. It verifies the returned command result, exact original review prompt in the native transcript, joined cleanup, refreshed Worker diffs and receipt replay without another input or automatic comment resolution. Initial reviewed changes are fixture-authored; resulting changes are native-owned writes. The model responses come from a scripted loopback provider, so this evidence cannot establish hosted-account inference, another harness, another platform or UI execution of the same native scenario.
