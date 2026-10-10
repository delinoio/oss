# DeliDev native read-only Sidechat

## Scope

The feature's Sidechat uses the Go session, Worker, workspace and native harness
owners in `cmds/delidev-cli/internal`, authenticated `protos/delidev/v1` and
`packages/delidev-api-client`, with presentation in `apps/delidev`. Ordinary Fork
retains its independent native/workspace lifetime contract. Sidechat owns its
conversation/runtime and references its parent's workspace without owning it.

## Runtime and Language

Go owns admission, immutable snapshots, once-only native operations and cleanup.
React uses generated Connect Query. The initial closed native enforcement profile
uses pinned Codex `0.151.0`; unsupported native/authentication/history profiles
return typed unavailable guidance before accepting a job. Native permissions
establish read-only behavior; prompt instructions and Plan labels grant no authority.

## Users and Operators

Owners and paired clients explicitly create Sidechat and select findings to send
back. Only the parent's original authorized Runner Device performs native work.

## Interfaces and Contracts

`SessionService.ForkSession` with closed `ForkPurpose.SIDECHAT` and equivalent
`session sidechat` identify the exact completed parent revision/native turn.
`GetSessionFork` observes the original accepted job. Independent System capability
27 and Worker capability 16 must be negotiated; reservations grant no support.
The parent's actual account/connection/model/instructions and original snapshot
are retained separately from the immutable child enforcement overlay. Current
account eligibility is rechecked at admission, claim, publication and execution;
there is no rerouting or adoption of later parent/Agent edits.

Creation requires terminal native history, no live approval/response or owned
child, and independently joined process cleanup. Publish the child only after
one native fork, complete native-history comparison, exact read-only enforcement
and cleanup. Unknown sends/acknowledgments remain uncertain without replay.
Inherited context grants no new input, executable queue, pending approval or
usage charge. Child queue/transcript/execution state remains independent.

`SendSidechatFindings` and `session sidechat send` submit an explicit bounded
selection of complete assistant messages at exact child/message/parent revisions.
The server builds text from those original messages, then uses the existing
queue/explicit Steer admission rules and UUID-v7 receipt. No automatic merge or
unselected transcript transfer is permitted. Account switching, terminals,
forwarding, Git writes, PR automation and workspace mutations cannot expand a
Sidechat's read-only authority.

Cleanup and recovery jobs remain queued while any durable Sidechat dependent
exists. The primary work stream retains one earliest skipped predecessor cursor
and revisits it after an in-flight assignment completes, a store change or its
bounded heartbeat. Later independent work may progress, but there is still only
one outstanding primary assignment. Every retry rechecks the original dependency
gate before atomic claim; a scan wake never grants native cleanup authority.

### Project requirements

- DeliDev Sidechat follows `cmds-delidev-sidechat-contract.md`. Preserve the parent fork-point account/snapshot, native read-only and external-write restrictions, metadata-only workspace reference ownership and durable dependent cleanup. Independent Fork lifetime and deletion ownership remain separate; reserved System 27 / Worker 16 numbers grant no support.

- Managed ChatGPT Sidechat follows the feature and `cmds-delidev-sidechat-contract.md#managed-chatgpt-sidechat--issue-1829`. Compose System 47 / Worker 26 with original Sidechat 27/16 and protected Worker 3. Freeze original generation/actor/source/instance; use the exact claimed Fork EXECUTE lease and server-owned Finish receipt before publication. Recheck managed authentication plus read-only enforcement before Fork/input/Steer/compaction. Preserve joined process/plaintext cleanup, original account/history, uncertainty and dependent deletion. Independent subscription Fork stays unsupported; no migration/new login. Fixtures do not establish native/account/platform acceptance.

- Sidechat same-question retry follows `cmds-delidev-sidechat-contract.md#same-question-retry--issue-2061`: System 57 / Worker 31 compose original 27/16 and managed 47/26/3. Preserve one direct text-only question, exact actor/revision/turn receipts, immutable child/snapshots, original metadata-only workspace reference, captured Worker/native authority, atomic current-answer publication and all-generation joined cleanup within existing bounds. Never replay uncertain native work, create another child or add a migration.

### cmds/delidev-cli constraints

- Native read-only Sidechat additionally follows `cmds-delidev-sidechat-contract.md`. Keep original account/snapshot provenance separate from the immutable child enforcement overlay and reference parent workspace roots without ownership. Parent deletion/storage cleanup must durably stop and join every dependent child before removing parent files; independent Fork lifetime remains unchanged.

- Managed ChatGPT Sidechat follows the feature and `cmds-delidev-sidechat-contract.md#managed-chatgpt-sidechat--issue-1829`. Compose System 47 / Worker 26 with original Sidechat 27/16 and protected Worker 3. Freeze original generation/actor/source/instance; use the exact claimed Fork EXECUTE lease and server-owned Finish receipt before publication. Recheck managed authentication plus read-only enforcement before Fork/input/Steer/compaction. Preserve joined process/plaintext cleanup, original account/history, uncertainty and dependent deletion. Independent subscription Fork follows the separately negotiated profile; no migration/new login. Fixtures do not establish native/account/platform acceptance.

### cmds/delidev-cli/internal/cli constraints

- `session sidechat` requires independent server support before purpose submission, preserves original parent workspace and accepts no Local override. `session sidechat send` freezes exact selected reply and parent revisions under one request; receipt retry never infers or automatically Steers. Follow `cmds-delidev-sidechat-contract.md`.
Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- Sidechat creation and observation retain the native fork 145-second command deadline; selected findings submission retains the immediate ordinary deadline. A bounded wait failure returns the original accepted job identity.

### cmds/delidev-cli/internal/domain constraints

- Sidechat uses a closed Codex read-only overlay of the complete original API account snapshot, with no child-account or permission expansion. Preserve the separate parent snapshot, version-3 fork seed and ordinary omitted-purpose bytes; follow `cmds-delidev-sidechat-contract.md`.
Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- Permanent-deletion ownership envelopes retain the original unpublished Sidechat child ID only for a Fork copy. Their dedicated strict JSON bound is 4 MiB; public command JSON retains 1 MiB. Preserve the 4,096-copy bound and reject malformed or extra authority.

### cmds/delidev-cli/internal/harness/codex constraints

- Private Sidechat uses the closed pinned read-only profile under `cmds-delidev-sidechat-contract.md`. Pin restrictions before launch, reject inherited MCP/plugin/hook/notification authority and independently verify normalized effective features before Fork, after binding and before each turn/Steer. Preserve original model/provider/effort/tier while applying only read-only, network-disabled, approval-never permissions. Approval grants/amendments cannot expand this profile. Its disabled Goals feature has a source-proved empty boundary and no native goal/get authority; ordinary Fork retains that read. Private validation grants no public capability or Worker admission by itself.

- Manual Sidechat compaction rechecks the complete closed config/read and normalized experimental feature inventory immediately before claiming or sending its one native action. A changed MCP, hook/plugin/notification, sandbox or feature observation cannot inherit the earlier turn's read-only authority.

### cmds/delidev-cli/internal/server constraints

- Sidechat admission/publication/execution require the original parent, account, completed native boundary and Worker capability 16. Selected complete replies use exact child/message/parent revisions and the ordinary queue receipt. Parent storage retirement and deletion freeze complete child obligations before cancellation; retirement inspection never grants early cleanup. Follow `cmds-delidev-sidechat-contract.md`.
Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- Primary Worker dependency rescans follow the Sidechat/storage contracts. Retain one earliest skipped predecessor cursor for blocked cleanup/recovery, restore it on completion/store wake/heartbeat, and preserve later independent progress, bounded pages, original atomic dependent-retirement gates and one outstanding assignment.

### cmds/delidev-cli/internal/store constraints

- Sidechat publication atomically records its bounded parent dependency. Keep it until original native, database and backup retirement finishes. Synchronized complete parent/storage intents reconstruct only their original child plans across SQL rollback and response loss; compare immutable ownership inventories, never substitute current work. Follow `cmds-delidev-sidechat-contract.md`.
Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- Permanent deletion derives unpublished Sidechat child IDs only from original immutable failed/canceled Fork assignments. Persist the complete synchronized plan within its 4 MiB bound, preserving legacy omitted fields and original request/digest identities; no current child lookup can reconstruct ownership.

- Sidechat storage retirement uses the strict typed storage-input decoder, including large original recovery with no dependents. Its private wrapper retains the full existing 4 MiB deletion plan plus 4 KiB fixed wrapper headroom; publication/restart use the same bound. Preserve original actor, job/input digest and every child plan identity before native storage admission.

- Selected skill admission shares one 4,096-reference budget across the parent and its indexed dependent Sidechats, including current/retired queued bindings. Resolve original Sidechat Fork/index ownership; keep independent Forks separate. Check original input/edit transactions and Sidechat admission/claim/publication atomically. Deleting indexed children fence new package/Sidechat admission until confirmed native, database and backup retirement releases the index; absent SQL rows are not proof. Preserve plain input, exact receipts and original preparation cleanup. Keep the existing 4 MiB outer deletion envelope; no eviction, RPC or migration. Follow the sessions and Sidechat contracts.

### cmds/delidev-cli/internal/worker constraints

- Sidechat pins the Codex native read-only profile for source inspection, Fork and every Execute/Plan/Steer/compaction continuation. Reference original workspace roots without owning their removal. A publication/deletion race may release an assignment only after authenticated exact retiring-envelope comparison; retain native uncertainty and let the joined deletion lane prove cleanup. Follow `cmds-delidev-sidechat-contract.md`.
Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- After a failed native Sidechat fork joins its original process, discard only its original inode-bound unpublished metadata under bounded cleanup. Foreign or replaced metadata remains pending. Revalidate the closed Sidechat native configuration/features immediately before manual compaction claim/send.

- Permanent deletion joins every original process/journal before reconciling unpublished Sidechat metadata through the original job/parent/child claim. Never remove those claim/root paths through generic cleanup. Published-child deletion retires its exact claim only after original metadata removal; completed-proof replay requires all claim and metadata names absent. Decode deletion work with its dedicated 4 MiB bound; the Connect response allowance includes bounded JSON/base64 overhead.

### cmds/delidev-cli/internal/workspace constraints

- Sidechat uses `codex-sidechat-reference-v1` under `cmds-delidev-sidechat-contract.md`. Retain original preparation/manifest and native directory identities; child metadata owns no parent files, repositories or General Chat directory. Reads/execution revalidate the exact parent and reference metadata while permitting ordinary file edits. Preparation/storage/terminal paths cannot expand that reference. Parent deletion, preparation cleanup and source-removing storage require all reference metadata to be independently removed after joined child cleanup. Child removal checks the original metadata inode and sole manifest entry and never traverses source roots; unknown/replaced ownership remains pending.

- Failed unpublished Sidechat reference preparation rolls back only the original inode-bound metadata through independent bounded cleanup. Foreign or replaced metadata remains pending; referenced parent files are never removed.

- Sidechat preparation synchronizes a private 4 MiB original-job/parent/child/inode-bound claim in sidechat-preparations/ before manifest publication. Failure and restart cleanup remove only matching original metadata; missing, malformed or changed claims cannot adopt existing roots. Published-child deletion retires the matching claim only after metadata absence. Parent files and native thread ownership never follow from this claim.

## Storage

Use additive strict JSON on existing session/fork/job/checkpoint records and
atomic SQLite state/event publication, with no empty or unrelated migration.
Retain parent/boundary links, the complete original snapshot, child enforcement
profile and original Worker cleanup device. Private native runtime claims bind
the original job/input/instance/manifest/digest; public records contain no paths.

A Sidechat preparation owns only its private metadata. It references the exact
parent preparation/manifest and canonical workspace roots, owns no checkout or
General Chat files, and checks parent availability before reads/execution.
Parent file edits do not transfer deletion ownership. Parent deletion or workspace
cleanup durably closes descendant admission, stops dependent Sidechats and joins
their original native/runtime cleanup before parent files are removed. Keep
references/pending state until that join; unavailable/offline resources remain
visible rather than being declared deleted. Deleting the child alone preserves
every parent file, native history and independent ordinary Fork.

Before publishing child reference metadata, synchronize a private 4 MiB claim bound
to the original Fork job, parent, child and native metadata inode. Independent
failure cleanup and permanent parent deletion after process-owner joins use only
that claim. Restart cannot adopt an existing child root from an absent, malformed
or changed claim. The server retains the original unpublished child ID in the
immutable Fork copy envelope, without paths. Published-child deletion retires
its matching claim only after original metadata removal. Completed cleanup proof
checks both names remain absent; replacement metadata stays protected.

Parent and child deletion plans retain complete immutable descendant obligations
outside rollbackable SQLite. The original synchronized parent intent precedes
child pause/cancellation and is the only source for reconstructing a lost child
journal. Native copies, database removal and managed backup removal must all
finish before the dependency index is released. Ordinary independent Forks do
not enter that index. Workspace Cleanup/Recover freezes its selected Sidechats
and actor in the accepted storage assignment; its separate synchronized intent
retires only that set before native parent storage work. Canceling after that
intent cannot undo already accepted permanent child retirement. Canceling before
it exists preserves children. Preview and snapshot creation remain read-only
with respect to child lifetimes.

The Worker deletion-work read also accepts an exact original session/job pair
without pagination. It returns only that device's immutable retiring ownership
envelope after current instance authorization. This inspection can let the live
controller release a fenced assignment when native publication races deletion;
it is not execution completion or permission to bypass descendant/terminal
cleanup gates. The Worker independently compares original session, server,
pairing device, machine, instance, claimed revision and assignment digest before
continuing its controller. Missing or changed evidence retains recovery.

## Security

Codex Sidechat overrides native execution to read-only, network-disabled tool
sandbox and approval-never with user reviewer. Before any native fork/turn,
verify the effective merged profile, disable native lifecycle/plugin hooks and
external tool features, and reject inherited MCP/plugin/notification authority.
Use private homes and the existing Go-owned API relay. Native model networking
retains its original account route; shell tools receive no execution/proxy secrets.
Questions/approval answers cannot amend file/network permissions or turn a denied
tool into an allowed one. Never edit machine-managed configuration to force a
passing profile. Missing or changed native enforcement refuses execution.

## Logging

Use structured ownership IDs, closed phases/outcomes and stable error codes.
Exclude prompts, findings, filesystem/native paths, configuration bodies, raw
native responses and protected credentials.

## Build and Test

Use temporary SQLite/Git/native-process/provider fixtures for exact snapshot and
history preservation, once-only replay/response loss/restart, changed authority,
write/network/remote-tool/approval bypass refusal, child Stop/Archive and dependent
parent cleanup. Secret sentinels must not enter DB/RPC/log/history. Run Go race/vet,
`pnpm proto:check`, client tests/typecheck and desktop `pnpm test`. Distinguish actual
pinned native scripted-provider execution from real-account and installed-platform
acceptance in the PR/issues/CI; do not add repository evidence documents.

## Dependencies and Integrations

Compose the native Fork, execution snapshot, read-only workspace, account,
queue/Steer, interaction, usage and permanent-deletion contracts. Referenced
workspace roots and native permission proof remain original-Worker-owned.

## Change Triggers

Changes to native enforcement, parent dependency cleanup, public schema or numeric
ownership update this contract, affected scoped `AGENTS.md`, project index,
allocation ledger and generated bindings in the same change.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [Project index](project-delidev.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [Native Fork](cmds-delidev-forks-contract.md)
- [Sessions](cmds-delidev-sessions-contract.md)
- [Workspace](cmds-delidev-workspace-contract.md)
- [Storage and permanent deletion](cmds-delidev-storage-contract.md)
- [Repository defaults](repository-defaults.md)

- Sidechat creation uses the full native-fork wait deadline while findings submission retains its immediate bound. UI fork admission includes current workspace availability. Revalidate the closed native Sidechat configuration/features immediately before manual compaction claim/send. Failed unpublished reference preparation/fork rolls back only the original inode-bound metadata under independent bounded cleanup; foreign/replaced metadata remains pending. Parent deletion capacity counts only newly created dependent journals.

Check the 256-child Sidechat ownership allowance inside the original fork admission transaction before queuing native preparation, and recheck at native claim/publication. The existing queued/claimed/uncertain fork reservation serializes the single outstanding preparation against that parent inventory; a rejected capacity request creates no job or child authority.

Selected skill snapshots share one 4,096-reference admission budget across the
parent and its registered dependent Sidechats, including current and retired
queued bindings. Resolve the child through its original Fork metadata and the
retained parent dependency index; independent Forks remain separate. Input and
queued-edit acceptance check this closure in the original transaction, including
concurrent parent/child writes. Sidechat admission, claim and publication recheck
the same closure before adding child ownership. A deleting indexed child fences
new skill or Sidechat admission until confirmed native, database and backup
retirement releases its index. SQL purge alone cannot free its frozen outer-plan
obligation. Text-only input remains usable, and rejected preparation retains its
original durable cleanup owner. Do not enlarge the 4 MiB synchronized deletion
envelope, evict accepted references, add RPCs or add a migration.

## Inherited image references

Sidechat adds no image deletion owner. Readback requires the current nondeleting parent owner and exact original succeeded Fork job prefix. Validate its input digest, session, machine, Worker device and runtime. Later parent images, changed jobs and restored quarantined metadata grant no access. Parent deletion joins dependent cleanup before last-owner image removal.

## Managed ChatGPT Sidechat
System `MANAGED_CODEX_SIDECHAT_V1 = 47` and Worker
`MANAGED_CODEX_SIDECHAT_V1 = 26` extend the closed Codex Sidechat profile.
System 27 and Worker 16 retain API Sidechat ownership; protected subscriptions
retain Worker 3. Negotiate all original adapters before managed admission,
claim, publication and continuation. Capabilities describe implemented adapters;
the original actual native process must verify the combined managed file-backed
ChatGPT/OpenAI authentication and read-only enforcement before native Fork,
every child input/Steer and manual compaction. Ordinary independent subscription
Fork remains unsupported. No migration, new login or credential conversion is
introduced.

Admission freezes the original protected account generation in the Fork input.
Only its exact claimed Sidechat job, actor, source boundary, account connection,
Runner, paired device and current instance may Take an EXECUTE lease. Receipt
replay cannot distribute credentials again. Source inspection uses its original
credential-free retained home and validates the same managed/read-only profile;
credentials are materialized only in the new owned private child home outside
workspace roots. The built-in OpenAI provider preserves the original model route.
API Sidechat keeps its existing unregistered relay profile.

Capture the final original-account bundle, join the original native process and
descendants, and independently compare/remove/scan private plaintext before
protected Finish. Finish rechecks the original actor/job/source before vault
staging and in its final transaction. Its server-owned receipt records only
opaque Fork/account/device/instance/generation/Finish references. Publication
requires that exact successful Finish receipt, the settled current generation,
confirmed native history, immutable read-only overlay and workspace reference.
A Worker result contains only the receipt reference. Missing or changed proof
retains the original unpublished child and recovery; it grants no fresh Fork.

The child starts paused with its original account/model/instructions and exact
native prefix. Later execution uses the existing protected Take/Finish lifecycle.
Parent-dependent cleanup, independent child queue/deletion and explicit
revision-bound findings transfer keep their existing ownership. Logs contain
operation IDs, closed phases and stable codes, never credentials, prompt text,
findings, paths or raw native content.

Local synthetic RPC/process fixtures cover capability and generation changes,
protected delivery/Finish replay, publication fences and combined profile drift
before input/Steer/compaction. Actual installed-native, real-account and platform
acceptance is owner-skipped for this batch and remains unperformed; local
fixtures do not establish it. Record commands/revisions/results in PRs and CI,
not repository evidence documents.

## Session-tab presentation

A retained Sidechat is presented inside its original parent workspace as a typed
child-session tab. Its own conversation, Info, actions and findings keep the
original child ID and read-only/native restrictions. Closing a presentation does
not delete the child or its dependent ownership; retained drafts and controllers
survive and sidebar reopening selects the child under its parent. Independent
Fork lifetime and deletion ownership remain separate.

## Same-question retry
System capability 57 and Worker capability 31 implement same-question retry
through `RetrySidechatQuestion` and `GetSidechatQuestionRetry`. Compose them with
original Sidechat 27/16 and, for managed ChatGPT, 47/26 plus protected Worker 3.
Old peers receive unavailable guidance. Allocation records are recorded in the
owning feature alongside closed declarations and bindings; no migration or flag
is introduced.

Eligibility comes from exactly one distinct directly submitted child queue
record: accepted text with no images or selected skills and a succeeded,
cleanup-confirmed answer. Inherited parent questions and generated retry inputs
are excluded; a second direct question removes eligibility. Reject unsettled
parent/child execution, queued input, Steer, compaction, cleanup, recovery,
archive, deletion, stale revisions, unsupported profiles and exhausted existing
history/deletion bounds before native admission. No deferred retry is reserved.

The actor-bound UUID-v7 receipt freezes child/question revisions, parent revision
and the exact latest completed parent native turn. Receipt replay and observation
retain that generation after parent advancement, restart or a later direct
question. Serialize unresolved generations; uncertainty never authorizes another
Fork or input. Recheck original actor, account, connection, immutable model and
instructions, original Worker device/current captured instance, workspace
reference and native read-only enforcement before claim and publication.

Keep public child ID, title, original `ForkOrigin`, snapshots and parent dependency
entry immutable. Each retry owns one fresh private native Fork of its accepted
prefix. Verify the original metadata-only child reference without preparing it
again or creating another child. Import the fresh native history independently
from the completed predecessor that owns the existing child workspace lease.
Submit the retained question once through the existing native/protected execution
controller. Managed Fork still requires exact Take/Finish receipts and joined
original-process/plaintext cleanup before publication.

Add strict bounded JSON generation records, a current-answer execution pointer
and one active retry owner. The previous answer stays selected until atomic
verified successful completion. Settled failure keeps it; recovery and uncertain
publication retain the original owner. Previous answers, failed attempts, jobs,
usage and revision-bound findings remain immutable. Stop/Archive fence claims;
permanent parent/child deletion freezes and joins original plus every successful,
failed, unpublished and uncertain retry runtime before dependency retirement.
Independent Forks and parent files retain their separate ownership. Preserve the
4 MiB outer deletion envelope and 4,096-copy bounds; never evict history or expand
an envelope to admit another retry.

The question action uses the issue's exact English/Korean explanation. Its
noninteractive popover opens after 300 ms hover or immediately on focus, shares
`aria-describedby`, stays open across its button and popover, and closes on
Escape, departure, inactive presentation or unmount without moving focus.
Disabled actions remain focusable with localized guidance. Previous generations
use a read-only history disclosure over the original bounded transcript payload
window; the active answer remains visible during work. Capability uncertainty
retains the original request controller across presentation changes. There is no
new pending Cancel action or automatic findings transfer.

Log operation/generation IDs, closed phases and stable errors only. Synthetic
RPC/native-process fixtures and builds do not establish real-account,
installed-native or platform acceptance; report their actual limits in PR/CI
records rather than repository evidence files.
