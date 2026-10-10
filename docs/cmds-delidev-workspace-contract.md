# DeliDev Worker workspace contract

## Direct startup retry claims

[Direct startup](cmds-delidev-execution-startup-contract.md) adds an explicit no-send retry claim under the original session lock. A retry with an existing predecessor requires its exact closed execution claim, original process reconciliation and unchanged canonical workspace/Git identities. If startup failed before a claim existed, absence is usable only after server-confirmed no-send/cleanup proof and a complete no-execution-history check. It grants no foreign-claim adoption, filesystem deletion, automatic replay or new Local authority.

## Scope
`cmds/delidev-cli/internal/workspace` owns Worker-local Git inspection, reference resolution, and all-repository preparation. It is independent of server SQLite and never receives a server GitHub PAT. Preparation is dispatched by authenticated outbound Worker jobs and published atomically into session metadata. The Worker first Codex runner now uses the execution lease below; public first dispatch uses that lease; the bounded same-account Codex profile follows [the fork contract](cmds-delidev-forks-contract.md), while additional fork profiles remain separate pending boundaries; managed whole-workspace snapshots follow the storage contract. Record implementation status and validation in pull requests, issues and CI logs/artifacts under the repository validation policy.

## Runtime and Language
Go and the execution machine's installed Git. No harness or Git installation is performed automatically.

## Users and Operators
The single-user server submits validated workspace jobs to the selected Worker. Local workspace identity is the actual originating machine, not the computer currently viewing a session.

## Interfaces and Contracts
### Repository clone ownership

Repository Clone accepts only credential-free HTTPS, `ssh://` and SCP-style SSH URLs and one portable folder name. Reject passwords/tokens, query/fragment, controls, local paths and external Git helper transports. The ten-minute deadline covers Clone, validation and publication. Before the first network-capable command, the Worker asks Git for the effective source URL and rejects an identity-changing `insteadOf` rewrite. Managed clone profiles then isolate URL-rewrite configuration while retaining the computer's configured credential helpers and SSH settings. Execute Git with argv through the existing owned process boundary, the computer's existing Git credentials/SSH keys and no server PAT. Select `origin` explicitly so global Git Clone defaults cannot change the remote required for registration. Disable hooks through the platform null-device path, use a separate empty template, and disable recursive submodules and optional LFS smudging; retain full Git history without shallow/filter options. LFS payload hydration and submodule initialization remain explicit later Git operations.

Create an exclusive private staging wrapper in the canonical selected parent, then create the empty staging checkout and synchronize a separate original-job/request-digest/parent-inode/staging-inode/checkout-inode claim before launching Git. Generic Worker execution journals independently prevent a second Clone after restart or uncertainty. Recheck the original parent, wrapper and checkout before and after Git; a replacement cannot become completion authority. Validate the canonical checkout and publish it through the existing platform no-replace primitive; an existing destination, including an empty directory, is a conflict. Recheck original parent/staging and published checkout identity around publication and synchronize before success.

Failure cleanup requires the original durable claim and independently joined original processes. Claim the original staging name through a no-replace private removal rename, verify its native identity, and walk/remove through anchored directory handles without following links. Changed/missing ownership, incomplete process termination or cleanup retains files and recovery ownership. Publication transfers the final checkout to user-owned Local lifetime: registration failure, job retry and repository configuration deletion cannot remove or replace it. This staging exception grants no session/snapshot cleanup authority. System capability 31 and Worker capability 18 negotiate the complete durable flow with the recorded allocations. The server rechecks original local Worker/device and selected profile authority at claim and registration. Its original report transaction publishes repository registration and the job result together, without a frontend save. Failed registration retains the published canonical checkout metadata. Bounded private Git diagnostics distinguish authentication failure and timeout without retaining stderr or URLs.

Inspection accepts an absolute root, subdirectory, or linked worktree and resolves its canonical working-tree root, display name, remote names, and locally recorded remote defaults. It neither fetches nor returns remote URLs. When a save supplies an opaque expected source identity, the Worker reads the selected effective remote locally and rejects a mismatch without returning the raw URL. GitHub HTTPS/SSH forms share their established, case-insensitive owner/repository namespace; generic hosts retain transport, SSH user and absolute-vs-relative SSH path namespace so distinct server repositories cannot collide. Encoded path separators are rejected before identity derivation so escaped and literal repository namespaces cannot collapse. An invalid preferred remote fails. Default reference selection uses the configured preferred remote, otherwise `origin`, otherwise the sole remote; missing or ambiguous defaults require input.

Repository save admission and final validation require an unused global identity
for expected revision zero, including absence of permanent tombstones. Known
identity conflicts after acceptance atomically retain valid original child
inspection success and terminal parent failure; unexpected storage errors roll
back for exact retry. Never recreate a deleted repository or overwrite a newer
revision.

### Negotiated repository metadata

The feature adds optional `Inspection.github_repositories`, mapping an inspected remote name to only validated `{owner, name}`. The normal `Inspect` result stays unchanged. `RepositoryInspectionInput` can carry an internal opaque expected source identity for a repository save/import; it never carries the raw URL into Worker output. The Worker enriches results only after observing server support and the second attachment's accepted Machine capability. Legacy combinations omit the map and identity; the server also omits the identity input for an active Worker that has not negotiated `RepositoryInspectionMetadataV1`, preserving strict decoders and the original inspection flow. Missing GitHub metadata never blocks otherwise valid registration or invents a default branch. The CLI's legacy inspection raw output remains decodable. This does not promise compatibility for unrelated legacy consumers that reject newly advertised Machine capability identifiers.

Enrichment uses the existing owned Git command boundary for `git remote get-url --all -- <remote>`. Effective fetch URLs include local `insteadOf` expansion, without fetching, changing files/index/refs, contacting a remote or invoking transport/credential helpers. Only a single canonical `https://github.com/owner/name`, `ssh://git@github.com/owner/name` or `git@github.com:owner/name`, with optional `.git`, produces metadata. Credentials, explicit ports, foreign hosts, escaping, queries/fragments, controls, extra path components and multiple URLs produce no inferred entry. Cancellation, process/cleanup uncertainty and command failure remain failures rather than missing metadata. Raw URLs never leave the Worker parser or enter logs. The map is limited to 128 inspected remotes and the existing 1 MiB job envelope; keys and identities are validated before publication. Structured completion logs expose only phase and counts.

Server-side repository validation accepts Unix absolute paths even on Windows and Windows drive-absolute paths even on Unix. It preserves those remote path bytes without local normalization or filesystem access; the owning Worker remains responsible for native absolute-path, canonical-root and Git validation before configuration publication. Configuration-import inspection jobs use the same negotiated Worker capability gate as repository-save jobs and omit the opaque source identity for legacy Workers.

New Worktree preparation clones each repository's pinned credential-free remote URL into its own session-owned full-history Git store, regardless of connected Local folders. It provisions every configured preferred/base/starting remote name against that pinned URL and mirrors the initial clone's tracking refs for those aliases. It resolves independently configured base and starting references under the same restricted clone Git profile used for the initial clone; automatic fetch updates the exact selected remote branch before resolving its commit, and a failure blocks preparation without stale fallback. Explicitly disabled fetch uses the stored tracking ref. Configured local branches are materialized from the pinned remote tracking refs before resolution. New managed clones are detached at the resolved commit, with no working branch creation, and the final checkout keeps LFS smudging disabled. Identical base/starting references share the same resolved commit.

Local preparation uses existing checkouts and their current HEAD/tree, including a valid unborn branch before the first commit, with no fetch, branch change, commit creation, or worktree creation. A configured remote URL must match the Worker-verified source identity of the selected checkout before publication. It requires matching execution/origin machine IDs derived by the server from the creation client's secondary paired Worker authentication. General Chat creates an independent session-owned directory without Git. The primary path is the designated primary repository or the General Chat directory.

Preparation serializes by session while independent sessions can proceed concurrently. Git work is bounded and occurs outside database transactions. A durable ownership manifest is written before side effects; readiness is published only after every repository succeeds. Identical retries reuse a ready manifest; changed input cannot overwrite it. Partial preparation rolls back only the recorded owned resources after joined process termination. Managed clones require original native root commitments, including incomplete clone roots; unknown ownership stays pending. Failed cleanup retains a recoverable manifest; explicit retry cannot delete a ready workspace. Session Stop/Archive now cancel queued or claimed preparation through the owning job and retain ready workspaces. Active harness lifecycle and snapshot cleanup use separate product operations. Failure to acquire/inspect an existing ownership scope or persist its initial journal is uncertainty, not proof that cleanup completed; only confirmed rollback may permit another preparation attempt.

## Preparation recovery
`Manager.Recover` inspects the original session preparation under its per-session lock; it never repeats preparation or fetches. The Worker first verifies the exact original claimed job/revision/instance/digest against the private execution journal, with server evidence from its immutable assignment record. A missing or mismatched journal cannot authorize native work.

Recovery has a two-minute Worker deadline and cancellable process-index traversal between individually bounded native ownership checks. A ready manifest must match the accepted input and exact local paths, avoid replacement links, retain their original Git administration. New clones require independent internal `.git` and original native directory commitments; legacy linked worktrees still share their expected common directory and source registration. Managed Worktrees retain the original detached HEAD. Local instead compares its captured Git administrative identity and validates the current committed or unborn HEAD without changing user branches or files. Ready working changes are preserved. Git registration comparisons use native path separators and Windows case semantics; this is not evidence of native Windows runtime acceptance.

Incomplete preparation requires explicit cleanup. Its manifest must match the accepted ordered repository prefix. Legacy Worktree entries require deletion-owned paths and resolved commits. New clone prefixes may lack resolved commits, but deletion requires the original synchronized native root commitment. Local entries require the exact original source/path, `owned=false`, captured identity digest and valid committed/unborn preparation facts; cleanup removes only managed session metadata, never an original checkout. An empty or partially recorded Local prefix is valid when it matches the original request. Local origin authorization and original session/machine/workspace identity are rechecked by the server at recovery acceptance and successful result publication; invalid authority leaves recovery uncertain. A per-original-job proof in private `workspace-recovery` records the incomplete manifest before deletion and completion after synchronized removal. It survives removal of the workspace root, allowing a retry to reconcile Git registrations even when the root is absent. A complete proof or matching original terminal cleanup journal is required to treat absence as clean. These proofs belong to their session's managed data and participate in the coordinated permanent-deletion boundary defined in the [storage contract](cmds-delidev-storage-contract.md).

## First native execution ownership
`Manager.ClaimFirstExecution` is an internal Worktree/Local/General Chat lease for the already prepared first execution. It validates exact accepted/local manifest bytes, input identity, canonical paths, detached creation HEAD for Worktree, independent clone native directory/internal Git identities, or historical source common-directory/linked-worktree identities and registration while holding the same per-session lock as preparation/recovery. It never fetches, resets files, changes branches or creates a replacement workspace; existing uncommitted changes remain intact. The caller still requires an authenticated immutable execution assignment and current server/account/Worker authority. Local execution additionally requires preparation-captured Git administrative identity and the server-authenticated original machine; current user commits/branches and dirty files remain intact.

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

### cmds/delidev-cli/internal/cli constraints

- CLI workspace-read and local-review response-header deadlines must exceed the corresponding server observation/mutation limits; grouped submission must not inherit the default shorter command deadline. Keep bounded ordinary/stream connection behavior unchanged and test delayed actual HTTP responses.

### cmds/delidev-cli/internal/domain constraints

- Retain the closed legacy PR Activity validators in `pr_activity.go` under the Activity retirement contract. Preserve original problem versions, actor provenance and attempt outcomes; legacy records grant no live projection or new verification authority.

- Published PR feedback uses only the closed read-only GraphQL documents, complete independently paginated review/conversation/thread inventories and repeated inventory plus PR binding. Exclude pending drafts before projection, retain approved/dismissed published content, recompute body/edit versions independently from provider state, and validate exact IDs/URLs/parent references. Preserve unknown actors without App/permission inference; no observation grants durable handling or execution. Follow the integration contract.

- Remediation defaults use merge; rebase requires explicit effective server/repository policy and an exact expected-head lease. Manual requests bind exact decimal set/problem revisions and immutable source/content identities. Closed typed push proofs distinguish verified, unchanged and uncertain state; native success alone never means handled.

- Every immutable manual-fix execution assignment independently requires Codex Execute mode and explicit workspace-write or full-access permission. Initial request acceptance cannot preserve write authority after the selected Agent becomes read-only or returns to default permission before dispatch.

- The feature descriptive startup progress follows the startup/workspace/process/desktop/protocol contracts. Preserve original claimed job/device/instance/revision/server epoch, bounded applicable operation summaries, exact receipt replay and independent leases/native input/cleanup. Completion requires actual successful operations; telemetry is nonblocking, joined, metadata-only and grants no execution authority. System 74 / Worker 50 preserve System 43 / Worker 23 and all existing startup fields. Old peers retain coarse progress; keep original failures, controls, drafts, focus and availability precedence. No migration or native-host change.

### cmds/delidev-cli/internal/integrations/github constraints

- Bound each optional enrichment to three seconds and at most one quarter of the owning query's remaining deadline. Join canceled reads before returning. If either repeated optional inventory is unavailable, drop both workflow projections and compare/publish the complete ordinary observations; drift between two complete inventories remains a conflict. Recheck retained workflow proof in the final CI inventory after the final rules read; if that optional inventory becomes unavailable, drop the workflow family while still requiring the complete ordinary observation to agree. Parent cancellation cancels the whole query.

### cmds/delidev-cli/internal/server constraints

- Native source operations recheck current policy and workspace ownership without converting the completed source assignment. Preserve legacy installation identity and each operation's own capability contract; new execution admission independently requires Worker 23.

- Repository Clone uses owner/client admission with fresh original local Worker proof authenticated inside new acceptance, actor-bound exact receipts that remain readable after secondary Worker retirement and System 31 / Worker 18 negotiation. Revalidate original device/profile generation at claim and completion, atomically register the published checkout with its original report, and retain canonical metadata after registration failure. Listing uses System 32, explicit revision/page, protected PAT generation, joined profile cancellation and whole-page validation before owner filtering. Follow the workspace/integration/protocol contracts; no new migration or renderer follow-up registration.

- Session PR links follow the integration contract. Resolve actual PR/repository numeric and node identities through fresh explicit-profile detail reads; recheck project membership/revision, repository selection/generation and actor before atomic creation. Bound/deduplicate per session, retain links after Archive or configuration changes, and unlink only an exact session-owned revision. Actor-bound receipt replay cannot repeat lookup or resurrect a tombstoned link. Historical association metadata grants no execution, current access, evidence handling or remediation authority.

- Remediation policy persistence follows the integration contract. Validate server defaults and optional complete repository replacements identically; never merge false/empty/missing override fields with server authority. Keep stable external reviewer IDs separate from local Agent/machine references, inventory/remap policy-only machines in portable configuration and recheck dependencies at deferred publication. Saving metadata cannot dispatch work, reset a chain, consume attempts or authorize evidence handling; execution needs independently fresh prerequisites.

- Validate failed workspace recovery against the original action and retained artifacts too; preserve prior availability, zero confirmed removal and required exact snapshot metadata before settlement.

- Validate reported workspace preview and snapshot SHA-256 values as canonical lowercase hexadecimal before publishing metadata or availability. Malformed reports retain job/session uncertainty and cannot strand a stored workspace behind unusable digest metadata.

- Automatic PR remediation belongs in `pr_automatic.go` under the integration contract. Keep linked-project discovery, independent default-off kind/reviewer gates, bounded joined target lanes and stable-PR deduplication separate from ordinary execution. Refresh enabled source evidence once, traverse bounded ordered problem pages and count only fully eligible versions toward the 100-problem request limit; never mark denied or remainder versions handled. Reuse the common admission/Worker Git engine and recheck original discovery-link controls at final claim. Explicit Stop/Archive/Restore suppress replacement; only a positively settled automatic failure with confirmed cleanup may select a fresh session while its old queue stays paused.

- Explicit skill inventory and preparation share the joined original Worker workspace-read lane. Authenticate original owner/client, Worker device/instance and Agent revision; expose metadata only. Bind immutable package references into original acceptance receipts and reject plain edits or Steer that would lose selected bindings.

- The feature remote starting branches uses System 51 / Worker 27 under the workspace, desktop and protocol contracts. Freeze configured source and project/repository/machine revisions; only the original authenticated selected Worker owns read-only native Git discovery. Retain complete 10,000-branch/8 MiB inventory bounds with feature-only job/journal/receipt/transport headroom, protected native Git credentials, safe logs and joined process cleanup. Discovery grants no checkout/preparation/execution authority or migration. Creation preserves saved/manual starting references, independent overrides, comparison base, Local proof and exact uncertain retries; older peers retain manual flows.

- Repository saves check unused global identity and permanent tombstones at expected-zero admission and final validation. Known identity conflicts settle the original parent failure with valid child success in one report transaction; unexpected storage failures remain rollback/retryable. Follow the workspace contract.

### cmds/delidev-cli/internal/store constraints

- Workspace continuation is a separate private lease after exact closed predecessor and process-cleanup proof. Bind unchanged preparation provenance and canonical/Git administrative identity while preserving agent commits, branch selection and dirty files. Synchronize immutable metadata-only predecessor history before replacing current ownership; never reuse retired execution IDs, adopt existing process scopes or infer a fresh first execution from a missing latest claim when history remains. Version-1 claims without captured identity evidence cannot be silently upgraded after execution. The public continuation integration must also verify native thread/history/account/input authority before sending. Include execution history in the session's future coordinated deletion/backup boundary.

- OpenCode original-input observation must preserve arrival order, claimed input digest, immutable native selection, independently inspected project root and exact message/part/call ownership. A first assistant snapshot may already contain final metadata before its parts arrive; keep it provisional until a later completed update validates original part closure. Repeated payloads can carry that later boundary without duplicating accounting. Native idle, message finalization, tool failure and root terminal remain independent; `unknown`, `tool-calls` and original non-interrupted local tools require a successor even when the provider says `stop`. Preserve the exact post-cleanup content-filter error refinement. Reject identity reuse, changed applied input, text regression, unowned deltas and autonomous work after settlement; latch uncertainty without erasing earlier evidence. Bound retained work and keep returned private payloads independent. This observer cannot establish native storage, effective account/plugin readiness, interaction acceptance, another send or process replacement.

- Manual fixes retain optional original Git/project selection on existing attempts. Only original assignment-bound verified push plus successful native completion/cleanup can handle exact evidence; dismissal wins a race and handled audit cannot be erased. Missing/uncertain proof retains stable PR ownership. Cancel only an explicitly removed unstarted original input atomically; preserve legacy attempts without inventing push proof.

- Automatic PR attempts retain their original discovery link and exact decimal-string revision in existing documents. Recheck source session/project, pause/Archive/recovery and link replacement inside `StartPRRemediation` before charging attempts. Preserve lifetime counts and uncertain ownership; optional metadata grants no historical native authority and adds no migration.

### cmds/delidev-cli/internal/worker constraints

- Repository Clone requires separately negotiated Worker capability 18 and the original once-only job journal before owned staging/Git side effects. Report one bounded dedicated outcome that can preserve a published checkout plus closed failure; never rerun a started/finished clone on restart or replacement. PATs never reach jobs, Git environments or logs, and published checkout lifetime is user-owned Local. Follow the workspace and protocol contracts.

- Batch compatible native Git metadata queries within one owned process, preserving exact record boundaries, absolute administrative paths and object format. Keep independent before/after identity validation for every repository and the bounded workspace-read deadline; never cache observations across reads.

- Workspace file and directory reads anchor each relative path component to its opened parent directory. Verify opened identities against non-link entries before and after opening; enumerate a directory and inspect its children through that same stable directory. A raced internal link or replacement must not return another workspace entry's content. Keep regular-file previews bounded and Unix opens nonblocking for swapped special files. Live user-owned files remain mutable observations.

- Record implementation status and validation results in pull requests, issues and CI logs/artifacts under the root DeliDev validation policy; do not add repository evidence documents. Fixture success, cross-compilation, or explicit unsupported results do not prove real-harness/platform acceptance.

- Workspace recovery integration waits must cover the bounded native recovery and result-report phases, observe the exact recovery job and session, and fail on early Worker exit. Log only typed progress and elapsed time; do not impose an undocumented five-second native Git latency requirement.

- Follow `cmds-delidev-workspace-contract.md` for Worker Git work. Never fetch during inspection or Local preparation, silently use stale remote refs after fetch failure, run repository hooks during managed preparation, or delete an original Local checkout. Keep partial cleanup retryable and distinguish prepared workspace tests from complete session/process recovery.

- Repository writes require fresh inspection on every configured Worker. Commit the canonical configuration and coordinator success atomically after all results, rechecking expected revision and dependencies; validation failure must not partially publish settings. Default omitted automatic-fetch configuration to enabled.

- Session creation atomically queues its immutable workspace request; successful Worker results must bind the exact session/machine/input digest, ordered repositories, paths, references, commits and primary directory before publication. Before a first native claim, Stop/Archive cancel only that preparation: preserve claimed revisions/digests in a separate durable cancellation record, keep Archive pending until native completion/cleanup is confirmed, retain ready files, and retry only confirmed failed/canceled attempts with their original input. Invalid results, inaccessible ownership journals and Worker replacement/revocation retain uncertainty. Preparing/restoring never resumes input.

- Workspace recovery must match the immutable original claim and Worker journal before native work. Never reconstruct ownership from a changed job result, infer cleanup from an absent directory, repeat preparation/fetch, or delete a ready workspace. Bound/cancel reconciliation, validate exact paths and Git ownership, journal partial-cleanup proof outside the removed tree, synchronize removal, and publish proven recovery atomically while keeping dispatch paused. Preserve original journal evidence across recovery retries and retained assignment copies across completion; remove copies with their owning job.

- A first native execution workspace lease must match the accepted ready manifest and actual detached Git ownership, preserve dirty files, and share the preparation/recovery session lock. Persist its job/execution claim outside the workspace before native launch. A released lock, absent workspace or missing process index is not cleanup proof; only bounded owned-job reconciliation may close the claim. Never reuse a closed first claim to resend input or silently adopt an existing process owner. Later-turn/resume and explicit native recovery use their separately documented contracts.

- First-turn Worker execution requires an authenticated immutable assignment, the exact resolved installed executable, a fresh private retained runtime and the owned workspace lease. Generate the scoped bearer only in memory and retain its registration digest/request before RPC. Publish supported events durably and fail on unhandled families. Report success/failure/stopped native completion only after all publications are acknowledged and native/process-lease cleanup is verified. The server must match execution/input/thread/turn/cursor/outcome, preserve earlier pause/recovery, and use reference-only completion receipts. Worker loss keeps execution ownership and unacknowledged inputs uncertain; it never erases observed terminal facts or proves cleanup.

- Managed multi-repository Codex execution uses every original prepared repository path in order and the designated primary cwd. Validate canonical roots before native sends, exact effective runtime roots and complete non-broadened native writable roots; preserve requested native permission/network/tmp semantics. Never substitute a common parent or source checkout. Retain root evidence in private checkpoints and independently derive comparison roots from the accepted manifest on retention, continuation and completed-execution inspection, validating ownership of non-primary repositories too. Omitted legacy root evidence supports only the previously verified single cwd; it cannot authorize additional repositories. Local must additionally retain its independently authenticated originating Worker.

- Grok Build discovery requires its pinned private ACP profile and a preceding owned read-only native configuration inspection. Reject inherited project/managed/remote settings and external extensions before ACP; never bypass machine policy. Reconstruct empty private homes and documented compatibility/update settings, advertise no client execution capabilities, and send only initialize. Require its original correlated response plus the exact empty startup MCP inventory, bound and join both streams/writer/owned cleanup, and discard private native metadata. Authentication, sessions, prompts and callback replies require separate adapters; a source-hash version suffix or handshake cannot grant execution/account readiness.

- OpenCode project instructions use original absolute paths through native additive loading, before Agent templates, with pinned filename-family and cwd-to-worktree precedence. Keep project configuration/plugins and Claude compatibility disabled; bound filesystem-root-sentinel workspaces to their session directory. Reject unreadable, linked, nonregular or oversized sources without omission, retain private comparison metadata, and reselect/compare before native creation/input. Instruction changes latch uncertainty; restoration cannot authorize retry, and this boundary does not grant continuous filesystem isolation or restart authority.

- OpenCode Worker execution consumes an authenticated immutable first assignment or an exact version-2 checkpoint continuation, the selected executable and the independently owned workspace lease. Keep registration intent in the Worker job journal outside the fresh empty native runtime. Preserve a bounded original event prefix until live and stored input acceptance, then publish it once. The outbound stream owns native lifetime; targeted cancellation before acceptance terminates startup, while an accepted live input uses one bounded original abort and joined response controls. Owner-only cleanup cannot invent terminal reporting. Close native history/process, retain the separate original Worker checkpoint while holding workspace ownership through any original snapshot export, then close the workspace lease before durable reporting. Only the independently eligible Build/Plan General Chat or single-root Git snapshot and closed inline tool/interaction profiles defined below may produce a new version-2 report; the private file alone grants no later input or resumed process authority. Public first dispatch uses the same immutable OpenCode selection gate, exact pinned native protocol and Chat Completions provider/account authority. Multi-repository settings and Windows non-VCS root identity use their separate profiles; do not drop explicit roots/options. General Chat must independently exclude enclosing Git metadata.

- OpenCode `project.directories.updated` is a bounded ancillary inventory notification with exactly the original native `projectID`. Preserve its original event identity and frozen observation without granting workspace roots, changed project identity, file content, tool/terminal or configuration authority; malformed and foreign project notifications remain unsupported.

- OpenCode global Git project adoption follows the dedicated harness contract. Only an original positive single-root snapshot may observe the native transition from global to a new Git project during an already claimed replacement. Verify the sole original session/marker/slug/time/settings and closed current-project metadata at the independently owned root, stage only the old self-contained archive at an exclusive private destination, then compare complete history and recheck the exact project observation before input. Preserve original runtime bytes, source digest and prior-input boundary in a carried typed proof. Never rewrite native rows, fabricate a commit, accept a non-global identity replacement, import additional sandboxes/commands, repeat a failed adoption or overwrite later Local files. Recovery remains read-only and paused; other roots and native project transitions need separate evidence.

- OpenCode multiple-repository execution follows the native local-reference contract. Preserve the manifest's primary cwd and ordered additional repository UUID/path pairs, require disjoint canonical paths without unverified glob/interpolation spellings, and retain the complete workspace lease. The pinned dual-loader bridge may create only an exclusive private reference-only config with its fixed schema; global config remains empty and v2-visible project sources are refused. Compare full effective config and native Build/Plan permissions, recheck exact private bytes and paths, and bind ordered references in checkpoint metadata/settings and read-only recovery. Never clone remote references, invent a common root, restore secondary files from the primary snapshot or omit selected repositories.

- Every OpenCode owned execution must enforce the native project configuration isolation contract, including single-root and General Chat profiles with no references or additive instructions. The pinned v2 loader ignores legacy disable flags: refuse every `opencode.json`, `opencode.jsonc`, and `.opencode` entry from canonical cwd through the independently validated native root without reading or changing it. Do not substitute the clamped instruction root. Recheck after staging, before mutations and retention, preserve latched uncertainty, and retain the private reference bridge plus exact effective-policy checks. The guard may be relaxed only with verified native loader isolation.

- Public Claude root-content continuation follows the harness/session contracts: promote to v2 only after acknowledged eligible original success or settled non-aborted failure, clean input EOF, workspace closure and exact retained native history. Preserve original session/history identity, account connection, immutable settings and input mode; give every successor a fresh job/input/native turn and relay token. Verify the predecessor's binding, original accepted report, settled outbox and independently digest-bound checkpoint before native launch. Missing or changed evidence retains recovery without another send. Only the separately composed inline Read/question/effect/task profiles below may join root content; unsupported tools/callbacks, stopped/aborted/interrupted or permission-changed histories cannot inherit this profile, and v1 remains paused. Keep native loopback evidence distinct from selected hosted-account and platform acceptance.

- Claude multiple-repository execution derives ordered roots only from the accepted complete workspace manifest. Keep its primary cwd unchanged and pass each other canonical existing directory with its own native `--add-dir` argument, retaining empty setting sources and the selected permission/account. Bind ordered roots into native and Worker checkpoint digests; omitted roots preserve legacy single-root bytes. Recheck original manifest, workspace/process ownership, root list and current canonical directories on continuation and metadata-only completed-report recovery. Never derive root authority from the checkpoint, reorder/drop repositories, reconstruct native context, replay tools or restore secondary workspaces from another repository. Log counts and ownership only.

- PR startup phases follow the workspace contract. Synchronize original job/execution/session and preparation/manifest/target digests before identity inspection. Only the unchanged checking phase plus independent preparation-process cleanup and absent native eligibility may yield a closed pre-native rejection; unknown, ownership or persistence failures remain uncertain. Exact rejection replay performs no Git work, never revives the original job/execution and cannot erase history. Fresh identities cannot bypass an unresolved prior phase. Private phase evidence must be independently bound to the immutable Worker assignment before public reporting/recovery; it is neither a native completion nor resend authority.

- Assignment-bound PR startup rejection follows the workspace/sessions contracts. Require the live private error, unchanged original Worker operation journal, exact retained rejected phase/manifest and absent native eligibility. Server acceptance independently binds authenticated original ownership/assignment and no grant/progress, retains the original rejected input and snapshot, frees pending capacity and stays paused/not-started. Preserve automatic attempt charges and the original job reference on release. Decimal-string assignment revisions must preserve uint64 precision. Report replay and Archive/Restore cannot resend or grant recovery authority; interrupted `journalStarted` remains uncertain until separately authorized metadata-only recovery proves its original rejection.

- Internal PR session planning follows the integration/sessions contracts. Enumerate original linked candidates by persisted activity within one database snapshot, excluding pause/Archive/recovery, without inferring native workspace compatibility. New-workspace plans require explicit project/Agent/machine and current account/native eligibility, preserve companion repository policy and primary cwd, and never reserve accounts or advance routing. Share ordinary configuration and native selection validation; actual acceptance/dispatch must recheck rather than promote a read-only plan into execution or Git authority.

- Existing PR workspace matching uses only the private exclusive Worker read profile defined in the workspace contract. Anchor the original manifest and current actual path, verify exact head/branch/remotes and all local changes without fetching, and independently clean read-process ownership. Keep unknown Git access separate from a confirmed mismatch. Bind reports to original request/current Worker, then recheck candidate revision, project and stable association; pause/Archive/unlink cannot retain a usable match. Never promote a matching read into execution ownership, write permission, a startup rejection or an implicit Resume.

- Verified settled Claude failures may retain v2 checkpoints only with correlated original input, eligible inline history, unchanged permission, acknowledged settled callbacks, clean original EOF and independently confirmed native/workspace cleanup. Preserve failure and paused dispatch; only explicit Resume may claim a fresh execution for the oldest new input. Never resend the failed input, tools or replies. Lost-report recovery compares original immutable journals/checkpoint/history without native work, preserves the failed outcome and remains paused. Aborted/Stop, changed-permission and unproved child/background histories stay excluded; never upgrade accepted v1 history.

- Uncertain protected execution completion must also escape ordinary job-error reporting, retain its started claim journal and close/join the primary work lane. Independent workspace cleanup failures cannot erase that uncertainty; server stream loss fences the original execution lease before another account grant.

- Repeated Claude child-history inspection revalidates original file/sidecar ownership and deduplicates each exact child/leaf/projected-telemetry digest only after its receipt acknowledgment, including exact replay. Keep a bounded 65,536-entry execution-local identity set; independently verified new leaves or native metadata finalization remain distinct observations. Neither a duplicate read nor deduplication grants root completion or native cleanup.

- Repository metadata enrichment follows the feature and the workspace/protocol contracts: preserve the initial attachment and exact inspection input bytes, request metadata on the second attachment only after server support, bind support into the retained negotiation profile and require Machine echo before enrichment. Effective URLs stay in the owned local Git parser; never fetch, call helpers, return/log URLs or turn cancellation/cleanup failure into metadata absence.

- Direct startup leaves execution process-index creation to the successful workspace claim. Definite resolver failure may create only a fresh exclusive, parent-synchronized empty index. Gate reconciliation on this attempt's ownership before inspecting or changing a retained scope; failed creation/synchronization and original-history/publication uncertainty cannot grant retry. Report Launch after successful resolution and Initialize at actual native opening.

- The feature descriptive startup progress follows the startup/workspace/process/desktop/protocol contracts. Preserve original claimed job/device/instance/revision/server epoch, bounded applicable operation summaries, exact receipt replay and independent leases/native input/cleanup. Completion requires actual successful operations; telemetry is nonblocking, joined, metadata-only and grants no execution authority. Close callback admission before draining accepted reports within one aggregate 1.5-second deadline, then cancel and join the original reporter. System 74 / Worker 50 preserve System 43 / Worker 23 and all existing startup fields. Old peers retain coarse progress; keep original failures, controls, drafts, focus and availability precedence. No migration or native-host change.

### cmds/delidev-cli/internal/workspace constraints

Follow the root and parent instructions and cmds-delidev-workspace-contract.md.

- Verify and synchronize every recoverable copy before claiming any source removal. Cleanup never pushes, removes original Local checkouts or overwrites foreign destinations. Restore all repositories atomically and preserve exact execution-lease identity.

- Retain private bounded cancellation/recovery journals and source data on incomplete outcomes. Logical removed bytes and retained snapshot cost remain distinct from measured free capacity. Successful snapshot inspect/restore/delete reports the pinned source bytes and the session's post-action retained inventory; snapshot deletion never counts as live-source removal. Tests use isolated temporary repositories and injected faults.

- Failed unpublished restoration cleans only its original operation-owned staging independently of caller cancellation; unconfirmed scratch cleanup stays recovery-required.

- Restoration ownership requires the original operation/snapshot-bound publication proof synchronized after the successful no-replace rename. Pending comparison bindings, matching foreign bytes and missing scratch never authorize recovery, execution identity or restored-workspace deletion; missing publication proof remains uncertain without replay. Version-2 publication proof binds stable native directory identity for the root, repositories and independent Git stores; recheck it at subsequent identity/recovery/deletion use. Legacy proofs cannot be upgraded from current paths. Ordinary file edits and commits retain ownership. Rewalk the renamed live root against the pinned complete snapshot inventory before publishing proof; changed post-rename bytes remain preserved behind pending recovery ownership.

- Bind cleanup removal authority to the verified snapshot source inventory, never a later mutable observation. Compare the claimed namespace to that inventory and restore the source name without replacement on mismatch before any unlink.

- Snapshot walks compare the root's opened/named identity, mode, size and modification time around enumeration and copying, as for nested directories. A late root entry or retained-handle mutation cannot yield a complete inventory or removal authority.

- Snapshot copying shares one remaining byte/entry budget across workspace data and every independent Git store. Reserve bounded manifest/config-rewrite byte headroom before payload writes, count Git roots once, reserve an entry before recreating an absent Git config, and charge only new overlay directories. Reject excess files before creating them; growing files cannot write beyond their metadata reservation.

- Reject partial-clone/promisor configuration and retained `.promisor` pack markers before creation and during inspection. Promisor-aware fsck cannot prove complete offline object closure.

- Failed snapshot capture retains recovery ownership when independent scratch removal is unconfirmed. Explicit recovery must remove the whole original staging tree before settling failure.

- On Windows retain the native file/directory symlink kind in the private inventory and recreate that type explicitly, including forward and dangling directory targets. Link-text equality alone cannot prove faithful restoration.

- Restored Git identity checks use the same offline read-only profile as snapshot inspection, including command-local Windows long-path support. Failure logging contains only closed phases, session/repository IDs and stable codes, never paths or native output.

- Every Worker-owned Git invocation enables Windows long paths per command, including initial preparation; never modify source Git configuration or depend on ambient settings. Native failure logs contain only ownership IDs, stable cause/code, exit status and closed read-only/offline flags, never argv, paths or native output.

- Independent linked-worktree Git stores retain common branch reflogs and overlay only colliding files from the selected worktree administration, including its HEAD log. Reflog-only unpushed/reset history must remain recoverable without the original Git store.

- Only snapshot create/cleanup and restore may create scratch. Persist an external versioned operation/request/preparation/snapshot-bound claim with native root identity after exclusive directory creation and before copying. Direct cleanup, explicit recovery and permanent deletion must verify this proof through an anchored remover; missing, legacy or replaced staging stays protected. Preview never owns scratch. Keep claims in deletion absence inventories; later reappearing staging cannot be removed by generic Worker copy cleanup.

- Removal journals retain an independent 64 MiB bound for repeated path transitions while immutable manifests/claims keep their 8 MiB bound. Before journal capacity is exhausted, atomically replace only settled history with the equivalent active prepared/renamed/directory-mode replay; preserve original header/root/intent identities and fail closed on malformed/canceled compaction. Published create and cleanup snapshots retain recovery-required ownership after any later failure.

- Source observation shares one 8,192-entry/8-GiB allowance across the complete workspace and all original Git administration/object inventories, before opening or hashing each admitted payload. Oversized observation cannot publish partial preview authority. Permanent deletion inventories every original removal claim `.json` and append journal `.pending`, including completed-proof absence replay.

- New snapshot capture/observation reserves bounded private rename spellings for every ancestor, complete snapshot/Git wrappers and the native removal-root path before admission. Retain the original 4,096-byte replay path bound; reject insufficient headroom before publication/removal. Inspect the original `.git` entry before Git resolves administrative paths; regular linked-worktree pointers are supported but symlink administration is unsupported.

- Source observation counts an independent Git store already covered by the complete managed-root inventory only once. Its bytes and changes remain bound to the root digest; external original stores still consume the shared allowance.

- Recovery after the top-level removal rename may create a missing original claim only when its private namespace remains intact and matches the complete synchronized intent, and neither a claim nor its journal exists. Partial or fully absent namespaces require the preexisting original claim. Malformed or replaced claims stay uncertain.

- Snapshot observation and removal inventory bind the original native root mode as well as all child metadata. Reject a managed root that differs from its fixed private writable mode before snapshot publication or live-root rename; a stale preview cannot grant authority over changed root permissions. Preserve explicit nested-directory mode-transition proofs independently.

- Managed Worktree clones use the restricted clone Git profile for remote URL
  changes, inspection, PR preparation and automatic fetches. Add every configured
  preferred/base/starting remote name against the one pinned repository URL and
  mirror initial tracking refs without stale fallback. Validate Git's effective
  source URL before networking, isolate `insteadOf` rules, and retain only the
  configured credential-helper and SSH settings in the restricted environment.
  When Windows adds the command-local `core.longpaths` setting, append it to
  the existing indexed `GIT_CONFIG_*` entries; never replace retained
  credentials or SSH configuration with a second config count.
  One ten-minute context covers the complete clone, validation, resolution and
  checkout flow; never widen the ambient transport policy for an alias or later
  fetch.

- Selected skill copying requires an original server preparation proof. Cleanup uses the joined keyless lane and original private file/root intent without current HOME, inventory or replacement-device authority.

- Prepared skill inventory shares one observation deadline across every accepted root: the earlier of the original deadline and fifteen seconds after observation admission. Retain the original skill context for package enumeration and the existing preparation/cleanup branches. Do not renew the budget per repository or relax ordinary workspace-read guards.

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

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

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
modifying source repository configuration. If the command already carries
indexed `GIT_CONFIG_*` entries for retained credentials or SSH settings, the
long-path entry is appended to that same indexed set. Native failures expose only stable
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

The feature adds private Worker-local whole-workspace snapshots, preview-bound
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

## Independent fork workspaces

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

### Remote source and independent clone lifetime

Repository saves require a credential-free remote URL and allow no checkouts. Registration performs no remote connection or clone; its durable save job completes atomically without inspection children when no folder is linked. Names may be suggested from the URL and edited. Optional folders retain existing authenticated inspection and save revalidation. When GitHub metadata is present, its owner and repository name must match the parsed canonical GitHub URL. Immediate Local Clone registration stores its original URL and transfers the checkout to user ownership.

Every new Worktree uses `remote-clone` with an immutable URL, empty checkout and capability 19 on the selected Worker. Explicit Local uses `local-checkout` with the URL and exact authenticated original machine/folder; missing folders block Local without fallback. Session and schedule preparation use the same acceptance and immutable ordered request. Initial clone is mandatory even when automatic fetch is off; that preference controls only later exact-reference fetches. Missing/default/ref/fetch failures never substitute stale values. Git credentials/helpers and SSH belong to the selected Worker; server GitHub PATs remain metadata-only.

Reuse immediate Clone's argv/credential-free transport restrictions, empty template/null hooks, disabled recursive submodules/LFS smudging and ten-minute deadline. Clone full history once per session/repository, with no shared cache. Persist the manifest and exclusive root/native commitment before Git; publish readiness only after all repositories resolve and detach. Identical completed requests reuse the original result, while loss/restart requires the original job and manifest recovery; interrupted/unconfirmed requests cannot repeat cloning. Process uncertainty preserves owned files. Replacement directories or missing native proof never authorize cleanup.

Managed clones use independent `.git` directories. Execution and continuation leases, preparation recovery, snapshot/restore and permanent deletion validate that ownership. Restore uses its existing independently synchronized publication proof rather than adopting old inode commitments. Fork derives `independent-fork` from a new managed or Local source only through the original closed parent: copy the complete bounded Git object/ref/reflog inventory into a fresh owned Git directory, pin parent HEAD/index/dirty files and set the original remote URL. Transport cloning is not a complete inventory copy: unreachable and reflog-only objects must survive as independent bytes. Common administration is copied with only the selected linked-worktree administration overlaid; unrelated worktree registrations and their external pointers are excluded. No hardlinks, alternates, promisor objects or external config includes can replace this copy. Its child lifetime remains independent of parent cleanup. Sidechat continues to own metadata only and blocks parent removal until its own joined cleanup. Legacy accepted linked worktrees and original Local lifetimes remain unchanged.

## Remote starting-branch discovery
System 51 and Worker 27 independently negotiate `REPOSITORY_BRANCH_DISCOVERY_V1`.
`DiscoverRepositoryBranches` accepts only a request identity and original project,
repository and selected machine IDs/revisions. The server freezes the configured
credential-free source, source identity and preferred remote (`origin` only under
the existing managed-clone default). Callers supply no URL, checkout path or Git
command. Discovery grants no preparation or execution authority and adds no migration.

The existing authenticated Worker job/claim/report lifecycle binds the original
machine, process instance and paired device. Revalidate configured membership,
source and revisions before claim and successful result publication. Stale queued
jobs fail without native work; stale reports settle as failures without publishing
inventory. Receipt replay retains the original operation. New explicit refresh
uses a new request identity.

Only the selected Worker runs read-only `ls-remote --heads` in its private state
scope. Reuse managed-clone source rewrite verification, credential/SSH-only native
Git configuration, redirect/protocol restrictions, two-minute deadline, process
ownership, cancellation and joined cleanup. No server/desktop account credentials,
fetch, clone, checkout mutation or credential persistence is permitted. Never log
raw sources, native output or credential-helper content.

Validate complete sorted unique Git branch names. Bound native output and public
inventory to 8 MiB and 10,000 branches; overflow fails instead of publishing a
partial list. This job alone has 9 MiB JSON/journal envelope headroom. Its report
receipt, bounded transport and single-resource reader support the complete result;
other job inputs/outputs, page/event bounds and native Git output limits remain
unchanged. A dedicated branch reader must not widen the general document parser.

## Project behavior settings

Follow [project behavior settings](cmds-delidev-catalog-contract.md#project-behavior-settings) for schema-2 documents, continuous inheritance, explicit original project context, policy precedence, first-publication durable plan decisions and portable version 5. Preserve the original domain authority and uncertainty rules; no SQLite migration is introduced.
## Prepared skill inventory observation deadline

Prepared-session skill inventory reuses original workspace observation locks,
manifest comparisons and anchored root identity. Its thirty-second operation
context remains available for subsequent package enumeration. Before observing
roots, copy the request with one deadline equal to the earlier of its original
deadline and now plus fifteen seconds. General Chat and every repository share
that same deadline; later roots cannot renew the observation budget. Ordinary
workspace reads retain their sixteen-second admission guard. Skill requests
retain their thirty-one-second admission ceiling and reject expired deadlines.
Creation/unprepared inventory, selected-package preparation and independent
cleanup retain their original operation context and ownership checks. Follow the
[explicit native skills contract](cmds-delidev-sessions-contract.md#explicit-native-skills).

## Descriptive startup operations
The invocation-scoped typed observer in `startup_progress.go` reports actual
setup, per-original-repository inspection/clone/reference/checkout, final
verification and publication. Local omits clone/reference/replacement checkout;
General Chat omits repository operations. The server derives the applicable
bounded operation plan from the accepted request, including the original ordinal
and total count. A multi-repository group completes only after every applicable
original operation is observed complete. Success of a later stage cannot fill a
missing earlier observation. Callbacks carry no paths, remote URLs or Git output.
Worker reporting is nonblocking and independently joined; shutdown closes callback admission and drains accepted reports for at most one aggregate 1.5-second reporting deadline before cancellation and join; it changes neither
Git deadlines, original journals, leases, return values nor cleanup ownership.
See the [startup contract](cmds-delidev-execution-startup-contract.md#operational-startup-progress).

## Complete independent Fork inventory

Independent Fork uses one aggregate 256 MiB/100,000-entry copy allowance across
all selected worktree files, indexes and Git stores, with reserved config/HEAD
normalization headroom. Source roots and common/selected administration identities,
full file inventories and digests are frozen before preparation and compared
before Ready and across the native Fork boundary. Source inventory growth,
ref/reflog changes, missing objects or incomplete copying prevent Ready. Linked
worktree `logs/HEAD` overlays only that selected private history; common branch
reflogs and nonstandard refs retain their exact bytes. The detached child HEAD
is the immutable selected fork-point commit; setting it does not append or
replace the copied history. Git configuration relocates only the child's
worktree location and retained remote URL. Original repositories are unchanged.

Fresh child root and Git-directory identities are journaled before copying.
Each copied regular file has independent storage and synchronized publication;
Git fsck validates the complete local object and index closure before readiness.
All byte/entry limits also bound source rechecks. A source change uses the
existing original cleanup and recovery ownership; a foreign directory or
unproved process cleanup remains uncertain. Sidechat keeps metadata-only parent
references. Original Local sharing and legacy accepted linked-worktree lifetimes
keep their existing ownership boundaries.

Directory identity rejection logs a closed predicate for named shape, canonical
path, original open, open-handle stat, native file identity, final named stat or
original identity mismatch. These private structured diagnostics include no path,
file index or native error text. A diagnostic does not weaken canonical admission,
adopt a replacement directory or prove successful cleanup.

Git-reported absolute administration paths may use native-equivalent separators and casing. Before capturing a strict directory digest, retain the filesystem canonical spelling only after proving the same original opened directory identity. Reject relative paths, links, foreign aliases and replacement identities; normalization alone grants no ownership or cleanup authority.
