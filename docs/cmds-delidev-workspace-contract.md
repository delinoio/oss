# DeliDev Worker workspace contract

## Scope
`cmds/delidev-cli/internal/workspace` owns Worker-local Git inspection, reference resolution, and all-repository preparation. It is independent of server SQLite and never receives a server GitHub PAT. Preparation is dispatched by authenticated outbound Worker jobs and published atomically into session metadata. The Worker first Codex runner now uses the execution lease below; public first dispatch uses that lease; the bounded same-account Codex profile follows [the fork contract](cmds-delidev-forks-contract.md), while additional fork profiles remain separate pending boundaries; managed whole-workspace snapshots follow the storage contract. Record implementation status and validation in pull requests, issues and CI logs/artifacts under the repository validation policy.

## Runtime and Language
Go and the execution machine's installed Git. No harness or Git installation is performed automatically.

## Users and Operators
The single-user server submits validated workspace jobs to the selected Worker. Local workspace identity is the actual originating machine, not the computer currently viewing a session.

## Interfaces and Contracts
### Repository clone ownership

Repository Clone accepts only credential-free HTTPS, `ssh://` and SCP-style SSH URLs and one portable folder name. Reject passwords/tokens, query/fragment, controls, local paths and external Git helper transports. The ten-minute deadline covers Clone, validation and publication. Execute Git with argv through the existing owned process boundary, the computer's existing Git credentials/SSH keys and no server PAT. Disable hooks through the platform null-device path, use a separate empty template, and disable recursive submodules and optional LFS smudging; retain full Git history without shallow/filter options. LFS payload hydration and submodule initialization remain explicit later Git operations.

Create an exclusive private staging wrapper in the canonical selected parent, then create the empty staging checkout and synchronize a separate original-job/request-digest/parent-inode/staging-inode/checkout-inode claim before launching Git. Generic Worker execution journals independently prevent a second Clone after restart or uncertainty. Recheck the original parent, wrapper and checkout before and after Git; a replacement cannot become completion authority. Validate the canonical checkout and publish it through the existing platform no-replace primitive; an existing destination, including an empty directory, is a conflict. Recheck original parent/staging and published checkout identity around publication and synchronize before success.

Failure cleanup requires the original durable claim and independently joined original processes. Claim the original staging name through a no-replace private removal rename, verify its native identity, and walk/remove through anchored directory handles without following links. Changed/missing ownership, incomplete process termination or cleanup retains files and recovery ownership. Publication transfers the final checkout to user-owned Local lifetime: registration failure, job retry and repository configuration deletion cannot remove or replace it. This staging exception grants no session/snapshot cleanup authority. System capability 31 and Worker capability 18 negotiate the complete durable flow after the main-first allocation prerequisites. The server rechecks original local Worker/device and selected profile authority at claim and registration. Its original report transaction publishes repository registration and the job result together, without a frontend save. Failed registration retains the published canonical checkout metadata. Bounded private Git diagnostics distinguish authentication failure and timeout without retaining stderr or URLs.

Inspection accepts an absolute root, subdirectory, or linked worktree and resolves its canonical working-tree root, display name, remote names, and locally recorded remote defaults. It neither fetches nor returns remote URLs. An invalid preferred remote fails. Default reference selection uses the configured preferred remote, otherwise `origin`, otherwise the sole remote; missing or ambiguous defaults require input.

### Negotiated repository metadata

Issue #1142 adds optional `Inspection.github_repositories`, mapping an inspected remote name to only validated `{owner, name}`. The normal `Inspect` result and `RepositoryInspectionInput`/queued input bytes stay unchanged. The Worker enriches results only after observing server support and the second attachment's accepted Machine capability. Legacy combinations omit the map; missing GitHub metadata never blocks otherwise valid registration or invents a default branch. The CLI's legacy inspection raw output remains decodable. This does not promise compatibility for unrelated legacy consumers that reject newly advertised Machine capability identifiers.

Enrichment uses the existing owned Git command boundary for `git remote get-url --all -- <remote>`. Effective fetch URLs include local `insteadOf` expansion, without fetching, changing files/index/refs, contacting a remote or invoking transport/credential helpers. Only a single canonical `https://github.com/owner/name`, `ssh://git@github.com/owner/name` or `git@github.com:owner/name`, with optional `.git`, produces metadata. Credentials, explicit ports, foreign hosts, escaping, queries/fragments, controls, extra path components and multiple URLs produce no inferred entry. Cancellation, process/cleanup uncertainty and command failure remain failures rather than missing metadata. Raw URLs never leave the Worker parser or enter logs. The map is limited to 128 inspected remotes and the existing 1 MiB job envelope; keys and identities are validated before publication. Structured completion logs expose only phase and counts.

Server-side repository validation accepts Unix absolute paths even on Windows and Windows drive-absolute paths even on Unix. It preserves those remote path bytes without local normalization or filesystem access; the owning Worker remains responsible for native absolute-path, canonical-root and Git validation before configuration publication.

Worktree preparation resolves each repository's independently configured base and starting references. Automatic fetch updates the exact selected remote branch before resolving its commit; a failure blocks preparation without stale fallback. Explicitly disabled fetch uses the stored tracking ref. New worktrees are detached at the resolved commit, with no working branch creation. Identical base/starting references share the same resolved commit.

Local preparation uses existing checkouts and their current HEAD/tree, including a valid unborn branch before the first commit, with no fetch, branch change, commit creation, or worktree creation. It requires matching execution/origin machine IDs derived by the server from the creation client's secondary paired Worker authentication. General Chat creates an independent session-owned directory without Git. The primary path is the designated primary repository or the General Chat directory.

Preparation serializes by session while independent sessions can proceed concurrently. Git work is bounded and occurs outside database transactions. A durable ownership manifest is written before side effects; readiness is published only after every repository succeeds. Identical retries reuse a ready manifest; changed input cannot overwrite it. Partial preparation rolls back only newly owned worktrees. Failed cleanup retains a recoverable manifest; explicit retry cannot delete a ready workspace. Session Stop/Archive now cancel queued or claimed preparation through the owning job and retain ready workspaces. Active harness lifecycle and snapshot cleanup use separate product operations. Failure to acquire/inspect an existing ownership scope or persist its initial journal is uncertainty, not proof that cleanup completed; only confirmed rollback may permit another preparation attempt.

## Preparation recovery
`Manager.Recover` inspects the original session preparation under its per-session lock; it never repeats preparation or fetches. The Worker first verifies the exact original claimed job/revision/instance/digest against the private execution journal, with server evidence from its immutable assignment record. A missing or mismatched journal cannot authorize native work.

Recovery has a two-minute Worker deadline and cancellable process-index traversal between individually bounded native ownership checks. A ready manifest must match the accepted input and exact local paths, avoid replacement links, share the expected Git common directory and remain registered with its source checkout. Managed Worktrees retain the original detached HEAD. Local instead compares its captured Git administrative identity and validates the current committed or unborn HEAD without changing user branches or files. Ready working changes are preserved. Git registration comparisons use native path separators and Windows case semantics; this is not evidence of native Windows runtime acceptance.

Incomplete preparation requires explicit cleanup. Its manifest must match the accepted ordered repository prefix. Worktree entries require deletion-owned paths and resolved commits. Local entries require the exact original source/path, `owned=false`, captured identity digest and valid committed/unborn preparation facts; cleanup removes only managed session metadata, never an original checkout. An empty or partially recorded Local prefix is valid when it matches the original request. Local origin authorization and original session/machine/workspace identity are rechecked by the server at recovery acceptance and successful result publication; invalid authority leaves recovery uncertain. A per-original-job proof in private `workspace-recovery` records the incomplete manifest before deletion and completion after synchronized removal. It survives removal of the workspace root, allowing a retry to reconcile Git registrations even when the root is absent. A complete proof or matching original terminal cleanup journal is required to treat absence as clean. These proofs belong to their session's managed data and participate in the coordinated permanent-deletion boundary defined in the [storage contract](cmds-delidev-storage-contract.md).

## First native execution ownership
`Manager.ClaimFirstExecution` is an internal Worktree/Local/General Chat lease for the already prepared first execution. It validates exact accepted/local manifest bytes, input identity, canonical paths, detached creation HEAD for Worktree, source Git common-directory and linked-worktree administrative identities and registration while holding the same per-session lock as preparation/recovery. It never fetches, resets files, changes branches or creates a replacement workspace; existing uncommitted changes remain intact. The caller still requires an authenticated immutable execution assignment and current server/account/Worker authority. Local execution additionally requires preparation-captured Git administrative identity and the server-authenticated original machine; current user commits/branches and dirty files remain intact.

After preparation-process cleanup is proved, the lease creates a fresh private process index for the execution job and synchronizes a metadata-only `execution-claims/<session-id>.json` before returning. New claims use version 2 and contain session/job/execution UUIDs, the accepted manifest digest, a SHA-256 identity digest and active/closed state, never commands, paths, prompts or credentials. The identity digest binds the canonical primary directory and ordered repository/common-directory/worktree-administration identities without persisting their paths in the claim. The claim lives outside the workspace so a lost directory cannot erase native ownership. An existing job process scope or prior first-execution claim cannot be silently adopted. Active/corrupt claims block preparation and recovery even after the OS lock is released by Worker exit.

The native owner must close/join its client before closing the workspace lease. Lease close independently reconciles every process scope in that exact execution-job index, synchronizes closed state and releases the session lock. Missing process evidence or unproven cleanup preserves the active durable claim and a recovery error while releasing the OS lock. Failed synchronization also returns recovery-required, even if the closed write landed after proven native cleanup; it never authorizes repeating the first execution. Repeated close returns the same result. Other sessions remain independent, and files are retained. A closed first claim never authorizes resending the initial input. The Worker now holds this lease across its accepted first Codex assignment through native closure and terminal cleanup reporting. The private continuation lease below now permits retained agent commits; the server/Worker now integrate its native history and current-account checks, while explicit reconciliation of interrupted native claims remains pending. Public first dispatch revalidates the original preparation manifest without reading remote paths on the server.

Grok's first-text runner also uses the first-execution lease for owned General Chat, with one fresh private native runtime and no repository adoption. Its original summary/idle/session close/removal and process/history comparison precede terminal publication, then independent lease closure precedes durable job completion output. A changed workspace claim after native success erases completion output and retains uncertainty. Cancellation joins owned processes; the separate original Stop composition below is required for native Stop proof and grants no continuation. Grok repository roots, instructions and continuation require separate native profiles; no additional root is silently omitted.

## Continuation workspace ownership

`Manager.ClaimContinuation` is a private lease primitive for a fresh execution/job following one exact closed predecessor. Its caller must separately validate immutable server configuration/account/input scope, native thread history and prior terminal acceptance; this filesystem operation alone cannot resume a harness or authorize another prompt. It takes the original preparation request/manifest and prior job/execution IDs, and uses the same session lock as first execution, preparation and recovery.

Before advancing ownership, require the exact current closed version-2 predecessor, recheck every indexed predecessor process scope for owned cleanup, and validate unchanged accepted/local manifest bytes and the original identity digest. Worktree HEAD may now contain agent commits or a selected branch, and uncommitted/untracked changes remain intact. The canonical owned paths, same source Git common directory, same linked-worktree administrative directory and registration remain mandatory. No fetch, reset, checkout, branch creation, commit or file deletion occurs. For Worktree, preparation/recovery and first execution continue to require the original detached creation commit; continuation cannot weaken those checks or silently adopt another registered worktree under the same path.

Before replacing the current claim, synchronize a metadata-only immutable copy of the closed predecessor at `execution-history/<session-id>/<execution-id>.json`, then create the fresh job process index and synchronize the new active claim with both predecessor IDs. Existing history must match exactly; old execution IDs and existing process owners cannot be reused. Failure to retain history prevents ownership advancement. A lost latest claim with retained history cannot authorize a new first execution; this check reads at most one history entry. Concurrent/replaced Workers cannot continue over an active, missing, mismatched or uncertain predecessor. Cleanup of the successor uses the same independent process reconciliation and idempotent lease-close boundary, preserving earlier history.

Version-1 claims remain readable for their original cleanup/recovery checks but lack the captured continuation identity digest. They cannot be automatically upgraded into continuation authority after files may have changed; explicit native/workspace recovery is required, without replaying the first input. History belongs to the session's managed metadata and must participate in coordinated permanent deletion and backup/restore; it is never removed by Stop/Archive. These lease fixtures alone do not establish native acceptance; the session contract owns public continuation integration. Record installed Codex evidence separately in pull requests, issues and CI logs/artifacts.

Session terminals use `WithTerminalDirectory` under the
[terminal contract](cmds-delidev-terminals-contract.md): a bounded anchored
observation verifies the complete original ready manifest and primary/General
Chat directory without taking the agent lease. Independent read-only process
ownership is reconciled after verification; a terminal receives its own native
owner and never changes workspace deletion ownership or secondary roots.

Directory-replacement fixtures retain each platform's actual protection: Unix
allows renaming the opened directory and rejects its changed identity after
launch; the pinned Go Windows root handle prevents that rename while anchored.
The Windows fixture requires the specific sharing violation and verifies that
renaming succeeds after the launch scope closes its anchor. Neither fixture
weakens the complete original manifest or primary-directory checks.

## Storage
The Worker owns private `workspaces`, `locks`, `execution-claims`, `execution-history`, and empty hook directories under its explicit data scope. UUID-v7 session/repository IDs derive managed paths. Manifests record original checkouts separately from deletion-owned paths. Cleanup recomputes owned paths from identities, reconciles Git registration even when a directory is absent, and never removes original Local checkouts. These local resources intentionally override the repository R2 default.

Canonicalize the private Worker root before creating ownership paths. Git's registration namespace can resolve parent aliases (including macOS `/var` to `/private/var`); comparing it to an unresolved path can leave an orphaned Git registration after rollback. Parent-alias regression tests must verify both filesystem removal and Git registration removal.

## Security
Git receives a bounded system/Git/SSH environment, not inherited API keys, server authorization, or repository-redirection variables. Authentication uses the Worker's prepared Git credential helpers/SSH agent. Interactive Git credential prompts are disabled. Preparation disables repository hooks for its own Git invocation without modifying user configuration. Worker stream loss/revocation and heartbeat expiry cancel active owned Git work before further assignment. Canceled inspection retains the cancellation classification rather than being reported as an invalid checkout. Raw Git stderr and remote URLs do not enter diagnostics. Reads reject replacement links at private workspace roots.

## Logging
Structured preparation start/ready/failure/cleanup records contain session and machine IDs, typed workspace mode, and safe failure codes. Execution claim success/failure and cleanup logs include job/execution/session IDs, continuation selection and safe codes only. Paths, Git remote URLs, file contents, and secrets are excluded.

## Build and Test
Run `go test -race ./cmds/delidev-cli/internal/workspace` and package vet. Tests create real temporary Git repositories, local remotes, linked worktrees, dirty Local trees, and separate General Chat directories. Validate fetch advancement/failure, detached commits, multi-repository rollback, idempotency, cancellation, original-checkout preservation, and missing-default rejection. Continuation tests additionally use real commits/branches/dirty files, exact predecessor history, concurrent owners, changed Git administrative identity, lost current ownership, retired-ID refusal and legacy/missing/invalid cleanup evidence.

## Dependencies and Integrations
Worker jobs pass typed preparation requests/results over authenticated Connect. The server validates complete manifests against the immutable accepted preparation input and records ready/failed/canceled/uncertain state with the job in one transaction. Cancellation is persisted outside the claimed envelope so reconnect can deliver a precanceled assignment without invalidating its journal identity. The Worker gives each job an independent cancellation context; a late control cannot cancel the next job. Files remain on the Worker, with references and resolved commits recorded on the server. The private first-execution lease shares that session lock and prevents preparation/recovery from bypassing unresolved native ownership. The first Codex Worker runner uses that lease; public first dispatch now integrates it, while snapshots, terminal ownership, additional fork profiles and full native recovery remain additional lifecycle boundaries not implied by passing preparation/lease tests.

## Change Triggers
Update this contract, the command contract, project index, scoped AGENTS, and validation records in pull requests, issues and CI logs/artifacts when ownership, reference selection, cleanup, or Worker integration changes.

## References
- [Project](project-delidev.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)

## Native process ownership
Worker Git operations run through the [owned process contract](cmds-delidev-process-contract.md), with the accepted job or session UUID as owner. A canceled or failed Git operation cannot authorize workspace rollback until its owned descendants are proven stopped. Uncertain process ownership retains the cleanup-pending manifest; retry cleanup reconciles that owner's indexed native process scopes before removing owned files.


### Closed execution inspection
`Manager.InspectClosedExecution` now holds the existing session lock while independently checking one exact current closed version-2 job/execution claim, the original accepted/local manifest bytes and the continuation workspace identity digest. It requires both original execution-job and preparation/Git owner indexes before read-only Git checks, so those checks cannot recreate missing process evidence. It reconciles those indexed owners, rechecks the unchanged claim and returns an inspection handle. Closing that handle only releases the lock; it does not create or rewrite cleanup evidence, advance to a successor, fetch, reset files or launch a harness. Agent commits/branches and dirty results remain intact under the same owned repository identity. A retired predecessor, active claim, absent process index, changed workspace or live competing lease stays uncertain. Callers acquire the original publisher lock first and must independently validate native completion and server authority. This private inspection is a building block for execution recovery, not a public Resume or recovery operation.


### Multi-repository native execution
`Manifest.WorkspaceRoots` returns a fresh ordered path list for every prepared repository (or the isolated General Chat cwd), independently of the designated primary path. The Codex runner now consumes all multi-repository paths as native runtime roots after the original manifest and owned workspace lease are checked. Its working directory remains the project's primary repository even when that repository is not first in order. No common parent, source checkout or additional unmanaged path becomes a substitute root.

First claim, continuation and completed-execution inspection already validate every repository's canonical location, original Git common/admin ownership and registration. Multi-repository execution retains that complete gate: changed/missing non-primary ownership blocks a replacement claim, while valid commits/branches/dirty files on any owned repository remain intact. Native permission and checkpoint/root comparison rules are defined in the harness contract. Local creation now authenticates its originating Worker, and the shared native-root gate also applies to its existing checkouts.

### Local Git identity
Each Local prepared repository includes `local_identity_digest`: a canonical SHA-256 digest of its repository UUID and canonical common/admin Git directory paths, captured during read-only preparation. Ready-result validation requires this proof for Local and rejects it on managed Worktree records. Local paths remain exactly their configured canonical original checkout paths with `owned=false`; they need not be under the private workspace root. First/continuation/closed inspection reject missing or redirected Git metadata and changed worktree registration while permitting current commits and branch selections. Independent Local sessions explicitly share checkouts; their per-session journals and process claims remain independent, and removing managed metadata never removes a shared source tree. Existing Local preparation records without the digest cannot acquire native authority. The original-journal preparation recovery path now supports ready Local inspection and explicit partial metadata cleanup; native snapshots and additional fork profiles remain separate pending boundaries; bounded same-account Codex forks retain their independently validated copy and cleanup ownership.


### Unborn Local checkouts
`PreparedRepository.local_head` is a closed optional enum: omission denotes the existing committed-HEAD encoding; `unborn` explicitly records preparation before the first commit. Committed entries retain equal canonical base/starting commit IDs and their exact commit starting reference. Unborn entries require both commit strings and the starting reference to be empty, preserving the configured base metadata without resolving it. Unknown states, a missing marker with empty commits, fabricated commits and unborn Worktree results are rejected. The omitted field preserves existing committed manifest/checkpoint bytes; no SQLite migration or silent metadata upgrade occurs.

Read-only native inspection first tries the exact HEAD commit, then proves a valid symbolic `refs/heads/` target, exact missing-reference status and unchanged symbolic target. Native exit statuses are private evidence; launch, cancellation, timeout, ownership and output failures cannot become absence. Broken references, missing commit objects, invalid symbolic targets and detached nonexistent HEAD are refused. These checks use Git's reference backend, not direct reads of loose ref files. The command behavior is defined by [Git symbolic-ref](https://git-scm.com/docs/git-symbolic-ref) and [Git show-ref](https://git-scm.com/docs/git-show-ref).

First execution, continuation, closed inspection and ready preparation recovery accept the user's current committed or valid unborn HEAD under the same captured Git identity. A user-created first commit does not rewrite the original preparation or checkpoint. Index, staged/unstaged/untracked bytes and branch selection remain unchanged by these checks. Structured Local inspection logs include only session/repository IDs and whether HEAD is unborn, never paths, branch names, object IDs or contents.

Remote default-branch inspection ignores only the native `git symbolic-ref --quiet` exit status 1 ([Git status contract](https://git-scm.com/docs/git-symbolic-ref)); launch, timeout, output and owned-process cleanup failures abort inspection without publishing successful preparation.

Before an execution lease is issued, a reported owner-directory synchronization or atomic claim-publication failure rolls back only the attempt-owned empty process directory and its exact claim. Continuation restores the original closed predecessor and retains immutable history. Cleanup synchronizes restored/removed entries, rejects unexpected claim changes or any process evidence, and logs rollback uncertainty without paths. This synchronous pre-launch rollback does not infer safety for a returned lease, a crashed Worker, or an active retained claim.

### OpenCode project snapshot ownership

The pinned OpenCode API profile supports checkpoint continuation and completed-report inspection for one prepared Worktree or authenticated Local repository through its separate positive snapshot proof. The native workspace/root is the independently verified primary Git directory, not a path supplied by a message. Existing common/admin ownership, registration, canonical-path and Local-origin checks remain authoritative. Original snapshot capture holds the execution lease through separately journaled Git object export after native cleanup; all export children join before lease closure and reporting. Export reads the original snapshot objects into a private self-contained archive without changing the source repository, index, branch or working files. Read-only recovery uses only that immutable archive plus normal owned workspace inspection; it never rebuilds missing checkpoint state. Multiple-repository OpenCode execution adds the separately verified local-reference profile in the harness contract. Every additional repository retains its exact ordered UUID/path and independent Git ownership under the same complete lease; the designated primary remains the only native cwd/snapshot root. Recovery compares all references against the original manifest and never restores secondary files from the primary snapshot.

### Claude multi-repository execution and retention
After complete original workspace validation and lease acquisition, Claude receives the manifest's primary path as cwd and every other repository as a native additional directory. The adapter preserves manifest order and checks canonical existing directories before launch or restoration. Original process and workspace cleanup remain separate from native history completion. Continuation and completed-report recovery derive ordered root authority from the accepted manifest, compare it with both retained checkpoint layers, and preserve secondary repositories as their original owned workspaces. They never synthesize missing native history, re-prepare a workspace, fetch, reset or replay a tool.

### Read-only session observations

The [file explorer contract](cmds-delidev-files-contract.md) adds bounded reads under the original accepted manifest without acquiring or changing the execution lease. Read-only Git validation uses session-bound `workspace-read-processes-v2/<session-id>/<read-id>` indexes; completed empty indexes are retired, and uncertain child ownership remains private recovery evidence. A separate cross-process per-session observation gate fences preparation/recovery/storage/permanent deletion through the entire anchored read and independently joined native-child cleanup. Execution retains its own lifetime lock, so views remain usable during a run. Busy admission returns conflict without workspace effects. Before destructive work, reconcile original session-bound read owners; unknown indexes remain recovery-required. Legacy flat read indexes have no session assignment and cannot be adopted or erased from current paths; their retention blocks new observation/storage pending recovery. Include the session-bound read namespace in permanent deletion absence checks, and reject new reads behind the original deletion tombstone. No read can reconcile, delete or replace preparation/execution process journals. `os.Root` anchors entry metadata and content independently of mutable absolute paths.

Grok first-text Stop, including after acknowledged input acceptance but before any output, follows the same independent execution-lease completion barrier. Original native interruption or completion racing Stop, joined native cleanup and an acknowledged terminal do not repair or replace a missing/changed workspace claim. The ordinary runner closes and verifies its original lease before retaining version-1 report output; failure preserves the original native terminal and recovery ownership. Neither stopped text nor a raced success supplies a continuation checkpoint or additional filesystem authority.


### Explicit original PR Worktree preparation

The internal accepted repository spec and prepared manifest may carry one copied `pr_target` from the integration contract. It must belong to that exact local repository UUID, name exact base/head commit references, explicitly enable fetch and use Worktree mode. Ordinary preparation remains unchanged when absent; Local/General Chat and multiple PR targets cannot acquire this profile. Result publication and partial recovery compare the original complete target and both immutable commits. Existing first-execution/continuation ownership binds these same original request/manifest bytes.

The Worker selects the configured preferred base remote, otherwise origin, otherwise its sole remote; ambiguous or absent remotes fail. Native remote URL expansion must identify the original base GitHub repository through canonical HTTPS, SCP-style SSH or ssh:// transport, with optional .git suffix. Embedded HTTP credentials, foreign hosts/repositories, multiple URLs and rewritten target URLs are rejected without exposing those values. Fork addresses derive from the same selected transport and original source identity. Git credentials still come only from the selected Worker's prepared native Git helpers/SSH configuration; server lookup PATs are never present.

Validate all fully qualified native branch names before networking, then independently read current exact base/head refs. Fetch only those objects with no destination ref, no configured ref mapping, tag following, pruning, submodule recursion, FETCH_HEAD write, automatic maintenance or commit-graph write. HTTP redirects are disabled. Independently verify both commit objects and repeat selected-remote, URL-expansion and remote-branch checks before creating the ordinary detached worktree. A moved/deleted head or base, changed destination, authentication failure or cancellation prevents readiness without stale fallback. Downloaded unreferenced Git objects may remain after a failed preparation; existing refs, index, branch, working data and other worktrees are retained.

The existing journal and process owner govern cancellation and rollback; original fetch descendants must be joined before cleanup. A ready identical retry and ready recovery perform no new fetch. This proves original preparation only: the manual PR fix profile below separately supplies fresh PR/Worker Git preflight and direct native-harness commit/push integration. Preparation does not change branches, merge/rebase, push or convey Git authority to an isolated AI runtime. Older ordinary manifests omit the additive field without changing their bytes.

Native command references: [Git fetch](https://git-scm.com/docs/git-fetch), [remote URL expansion](https://git-scm.com/docs/git-remote), [exact remote refs](https://git-scm.com/docs/git-ls-remote), [native ref validation](https://git-scm.com/docs/git-check-ref-format).


### First PR execution preflight

The common first-execution lease now revalidates each explicit PR-prepared repository after normal complete workspace identity checks and before creating the execution claim/process scope. While holding the existing session lock, inspect the actual prepared path rather than its source checkout's configuration. Require the original detached HEAD and no staged, unstaged, untracked or ignored content; preserve and report any separate local work. Read both current remote base/head refs through the exact canonical target transports and repeat remote selection plus local HEAD/branch/cleanliness after networking. Worktree-specific remote overrides cannot borrow the source checkout's earlier proof.

This bounded 30-second check runs only read-only Git under the preparation/session process owner, with fsmonitor disabled, no fetch and no changes to index, FETCH_HEAD, refs or files. Its owned children must finish before the ordinary execution owner is published. Failure leaves the prepared manifest intact and does not create an execution claim or native process scope. A separately authorized fresh attempt needs distinct job/execution identities and rechecks real facts. Structured start/ready/failure logs contain only session/repository IDs and safe codes.

The shared lease applies this check before any native runner starts a fresh PR-prepared execution. An ordinary continuation still uses its existing native-history/ownership contract; it cannot infer fresh remediation or push authority from this historical target. The manual PR fix profile below supplies a separate fresh per-attempt Git preflight for reused sessions and the direct-harness Git-auth/commit-push flow. No native Git read proves GitHub API identity, write permission or an actual successful push.

A failed private lease preflight is not a public resend grant. The assignment-bound reporting boundary below distinguishes a positively recorded pre-native rejection from uncertain native completion. Private fresh-identity claims cannot bypass this server-owned boundary.

### Current native matching for an existing PR session

The private Worker read envelope may select `pr_candidate` instead of a public file query. These profiles are exclusive: ordinary file reads reject the private field, and PR matching requires a zero file query plus an original Worktree/Local manifest and exact current Git target. General Chat and mixed/foreign scopes cannot fall back to a file read. This profile retains the existing fifteen-second read deadline, original manifest/root anchoring and separate read-process ownership; it neither takes nor rewrites the session's execution lease. Complete workspace identity is checked before and after observation, and independent owned read-process cleanup must finish before returning proof.

Inspect the actual prepared repository path using its original selected remote and Worker's prepared native Git authentication. Its current HEAD must equal the selected PR head; permit either detached HEAD or exactly the PR's head branch, rejecting an unrelated named branch even at the same commit. Require a clean index/worktree including untracked and ignored content, and independently verify original remote base/head operands, selected remote and local state again after networking. The shared first-PR startup preflight still requires detached HEAD. No fetch, checkout, reset, stage, commit or push occurs, and the read preserves original files, refs, index, FETCH_HEAD, manifest and active execution claim. An ordinary session may now match after later commits even when its original preparation commit differs.

The version-1 private result contains only original read/session/repository UUIDs, `matches` or `different`, exact whole-request SHA-256 digest and bounded UTC observation time. Confirmed state/branch/head differences remain distinct from inaccessible/unknown Git or uncertain ownership, which return a typed error without matching proof. The server validates the exclusive result against its original pending request/current paired Worker, then rechecks the original candidate revision, live project scope and exactly one original stable PR association. Concurrent pause/Archive/activity or unlink invalidates the candidate even after a syntactically valid Worker report. This observation creates no durable job, input, receipt, startup phase or native grant. Public Fix now composes this matching read with the separate manual profile below and execution-time revalidation; a successful read alone does not establish write access or authorize commit/push.

### Original PR startup phase journal

Before ordinary workspace identity inspection for a fresh PR execution, synchronize `pr-startup/<session-id>/<execution-id>.json` under the same session lock. The version-1 metadata record binds distinct original job/execution/session UUIDs, preparation/manifest/target SHA-256 digests, checking/passed/rejected phase, timestamps and a closed rejection code. It contains no paths, refs, repository/title/prompt content or credentials. Synchronize the session directory before the initial record. Normal non-PR workspaces and continuations do not create this journal.

Only the original unchanged checking record can finish. After successful workspace identity validation and the bounded PR preflight, independently reconcile the preparation process owner and confirm that no execution claim, original execution-job process scope or runtime was created. Persist passed before execution-claim publication. Persist rejected only for the closed conflict/missing-input/unavailable/canceled/resource-exhausted/invalid-argument reasons after that cleanup proof. Ownership, unknown-error or persistence uncertainty retains checking; missing native logs alone cannot establish rejection. The private typed error carries the original positive rejection, not a fabricated native completion.

An exact rejected request returns its retained original result without inspecting Git or repeating network reads, including after Manager replacement. Contradictory execution claims, job process scopes or runtimes block that replay. Restoring operands cannot revive that execution identity, and the original job cannot acquire a different execution identity. A fresh pair additionally checks the bounded same-session history: every prior record must be a valid original rejection; checking, passed, malformed or foreign records block another first execution. Retain at most 10,000 original startup records without eviction. Passed followed by interruption before claim publication stays uncertain rather than retryable. Structured settlement logs expose only IDs, phase and safe code. Include these records with future coordinated session backup/deletion. Explicit metadata-only inspection follows the sessions contract.


### Assignment-bound startup rejection reports

The Worker may report `pr-startup-rejected` only from the live private rejection error and the unchanged original `journalStarted` assignment. Independently read the original rejected phase and retained manifest under the session lock, with bounded metadata-only cleanup time even after cancellation. Compare exact phase bytes/digest and original preparation/manifest/target digests; require no native claim, execution scope/runtime, prior Worker output or publisher directory. No Git or native process runs during this read. A reconstructed generic error, missing operation journal or borrowed proof remains recovery-required.

The version-1 envelope binds server/device/instance/machine, original input/account/connection/configuration and exact assignment revision/body/input digests. Encode its unsigned assignment revision as a decimal JSON string. It contains no path, prompt, repository title or credential. Before acceptance the server independently compares the authenticated Worker, retained assignment, current claimed job/session/input, pure preparation/manifest/phase proof and absence of an execution grant/native publication. Its proof validation checks remote paths against the selected Worker OS, not the server host OS; only Worker-local preparation applies host filesystem path rules. Contradictory or incomplete evidence follows ordinary uncertainty. The accepted transaction and retained-store verification are defined in the sessions contract; neither grants another dispatch or native completion.


Explicit startup recovery reuses `ReadPRStartupRejection` under the original session lock; it never calls preparation, first-execution claim, process cleanup or Git. The separate Worker/server comparison must bind the original job journal and assignment/device before accepting this proof. A read cannot convert checking/passed/missing phase records to rejection, create a new record, repair a changed manifest or grant native eligibility.

## Bounded Git metadata observations

Worker-owned Git commands enable Windows long paths through a command-local
`core.longpaths=true` override during preparation and later observations, without
modifying source repository configuration. Native failures expose only stable
launch/exit classifications, exit status, owning IDs and read-only/offline flags;
argv, paths and native output remain excluded from logs.

Compatible administrative path and object-format queries share one owned
`git rev-parse` invocation. The parser requires the exact record count and
terminators, rejects ambiguous embedded delimiters and retains path spaces.
Source/linked-worktree identity, all-repository before/after checks, original
HEAD validation, filter checks and private diff administration remain mandatory.
No result is cached across observations and the 15-second read deadline remains.
This reduces repeated process/journal setup on Windows without relaxing native
ownership or accepting partial metadata.

## Preserved project-index implementation notes

The following source-backed notes were relocated from the project index at `12b33a2accaf`. Their historical qualifications and unresolved acceptance boundaries are retained verbatim.

First-execution configuration, initial account selection, input claim and per-Agent routing state share one durable transaction. The public first-dispatch coordinator validates current installation/account/workspace/Worker evidence and exact native selection in the same transaction as its immutable job. Eligible ready sessions dispatch automatically; paused/restored first sessions require explicit Resume, and failed checks consume no input or routing. Template contents/order are retained exactly, later edits cannot rewrite them, and current restrictions still apply.

An internal completed-execution inspection now correlates the original Worker operation/outbox/native checkpoint with current closed workspace/process ownership, preserving results without relaunch or report replay. The dedicated public execution-recovery RPC/CLI and Worker job now reconcile a retained completed execution atomically after Worker replacement, preserving outcome and pause until explicit Resume. Missing historical interaction evidence and safe surviving-process reattachment remain separate requirements.

## Managed storage ownership

Issue #1079 adds private Worker-local whole-workspace snapshots, preview-bound
manual cleanup and atomic restoration under the existing session/process locks.
Follow [storage operations](cmds-delidev-storage-contract.md) for the complete
job/receipt, bounds, independent Git stores, retained bytes, removal-intent and
explicit recovery contract. Original Local checkouts are ineligible. No source
removal precedes verified publication of every repository. Restore keeps the
canonical owned cwd and original logical identity through a private comparison
binding, with standalone Git administration replacing the removed linked store.
Active claims, unknown dependent resources and conflicting destinations remain
protected. This storage comparison grants no new native checkpoint/history
support; all existing execution and continuation evidence gates still apply.

Manual Fix now retains the ordinary execution lease through native closure and independent push verification. Its closed private Git bridge binds original source transport/executable/auth context and exact ref/base/head, exposes exact non-secret operands, claims one push synchronously before launch, joins native children and emits only original verified/unchanged/uncertain proof. Rebase conflict publication requires an original-head lease. Preserve original files and uncertainty after interruption; no bridge or Go controller automatically commits/pushes. See the manual-fix section of the integration contract.

Before publishing that capability, every privileged fetch/push and post-native
verification, the Worker rechecks the complete original execution lease's
workspace identity, including companion repositories and linked Git
administration. Current commits may advance under continuation identity checks;
replacement directories, symlinks or administration cannot borrow native
authentication or publication proof.

After native-client and bridge closure, push verification independently
reconciles the original native process owner before its first Git observation.
This barrier retains the active workspace claim and OS lock; final lease closure
rechecks ownership before recording cleanup and releasing them. Missing or
changed process evidence leaves push proof uncertain without observing Git.

The shared bridge command lock covers validation and the actual local child,
including commit, merge and rebase. A streamed fresh command grant keeps the
Worker request and lock alive until the sandboxed launcher acknowledges child
exit through its separately admitted authenticated completion endpoint. Parallel
push cannot claim an intermediate HEAD. Unknown or duplicate grants cannot
release an owner. Lost completion or cancellation makes the capability uncertain
and refuses later commands and clean bridge closure; the grant is never retried.
This private handshake provides ordering, not independent descendant cleanup or
an executable/configuration isolation proof.

The manual Git launcher's local capability connects only to a canonical
`127.0.0.1` listener owned by the original Worker. It carries no reversible Git
authentication context. The Worker keeps immutable scope/configuration and a
private push-claim MAC key in memory, serializes bounded cancellable commands,
validates closed local-command operands without executing them, and retains
native authentication only for exact network operations. The harness launcher
runs local Git and any repository helpers under its inherited native sandbox,
with a credential-free environment and the captured commit identity. Local
process ownership stays inside the original native execution's macOS coalition,
Linux subreaper or Windows job. Local Git uses ordinary fork/exec in the launcher,
never the generic process supervisor whose macOS launchd child would leave the
harness sandbox. Its command and output waiting are bounded; only independent
original native-owner closure proves descendant cleanup before push verification.
External input/output
file flags, configuration options, path traversal and pathspec magic are rejected. It cancels/joins the listener and commands before proof while keeping
the original execution lease. Changed configuration, forged metadata or uncertain
shutdown cannot grant handling or another push; process restart cannot rebuild
a missing original capability/proof key. This is a private Worker tool boundary,
not a server-side Git publisher or new public RPC.

The exact allowed `rebase --continue` uses a fixed noninteractive no-op editor to preserve the original commit message after conflict resolution. No harness editor or arbitrary interactive rebase is accepted; its eventual push still requires the exact original-head lease. Git editor precedence follows the [Git editor contract](https://git-scm.com/docs/git-var#Documentation/git-var.txt-GITEDITOR).

Permanent session deletion includes each original execution's `pr-git` capability
scope in the same independently checked Worker removal inventory. A restored
scope invalidates a previously completed deletion proof.

## Independent fork workspaces (#1092)

Follow the [fork contract](cmds-delidev-forks-contract.md). Optional omitted
`fork_source_id`/`fork_source_path` preparation fields preserve historical JSON.
The owning Worker holds the original closed execution inspection while deriving
actual HEAD commits, then copies each repository into a separate detached
`--no-checkout` worktree without fetch or checkout filters. It copies the exact
non-split index and bounded regular files, preserving original directory and
file permission modes independently of the Worker umask, including staged, unstaged, ignored
and untracked data. Source reads and destination writes use separate opened
filesystem roots; linked destinations are rejected before copying, and copied
files/directories are synchronized before the ready manifest. General Chat
copies its owned tree. Source and target hashes,
HEAD and index are rechecked after every repository and after native creation.
Explicit authenticated Local uses the same user-owned files only from an original
Local source. Managed source worktrees cannot become unowned Local child paths;
their parent retains removal authority, so those sources require independent copies. Links, nested repositories,
special files, split indexes and unborn Git are outside the initial profile;
refusal cannot silently drop data or publish partial preparation.
