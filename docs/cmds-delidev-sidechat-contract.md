# DeliDev native read-only Sidechat

## Scope

Issue #964's Sidechat uses the Go session, Worker, workspace and native harness
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

## Managed ChatGPT Sidechat — issue #1829

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
