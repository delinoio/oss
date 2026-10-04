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
