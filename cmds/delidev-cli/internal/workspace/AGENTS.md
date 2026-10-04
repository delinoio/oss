# DeliDev Worker workspace ownership

Follow the root and parent instructions and docs/cmds-delidev-workspace-contract.md.

- Worker-local snapshots and cleanup follow docs/cmds-delidev-storage-contract.md. Preserve ordered all-repository manifests, commits/unpushed history, index/worktree state, ignored/untracked regular files, modes and symlinks without dereferencing links. Reject unsupported special files, external Git object dependencies and undeclared nested Git administration, including directories and filesystem case aliases.
- Verify and synchronize every recoverable copy before claiming any source removal. Cleanup never pushes, removes original Local checkouts or overwrites foreign destinations. Restore all repositories atomically and preserve exact execution-lease identity.
- Retain private bounded cancellation/recovery journals and source data on incomplete outcomes. Logical removed bytes and retained snapshot cost remain distinct from measured free capacity. Successful snapshot inspect/restore/delete reports the pinned source bytes and the session's post-action retained inventory; snapshot deletion never counts as live-source removal. Tests use isolated temporary repositories and injected faults.

- Failed unpublished restoration cleans only its original operation-owned staging independently of caller cancellation; unconfirmed scratch cleanup stays recovery-required.
- Restoration ownership requires the original operation/snapshot-bound publication proof synchronized after the successful no-replace rename. Pending comparison bindings, matching foreign bytes and missing scratch never authorize recovery, execution identity or restored-workspace deletion; missing publication proof remains uncertain without replay. Version-2 publication proof binds stable native directory identity for the root, repositories and independent Git stores; recheck it at subsequent identity/recovery/deletion use. Legacy proofs cannot be upgraded from current paths. Ordinary file edits and commits retain ownership. Rewalk the renamed live root against the pinned complete snapshot inventory before publishing proof; changed post-rename bytes remain preserved behind pending recovery ownership.

- Cleanup/deletion recovery requires the original synchronized removal intent plus a matching verified namespace claim when both namespace names are absent. Persist the claim after inventory validation and before unlinking; an intent alone or filesystem absence is never verified removal. Version-2 claims also pin the native root identity; legacy claims cannot authorize new unlink. Remove only pinned inventory entries, verify their bytes/link/mode and anchored identity immediately before unlink, and retain changed/new entries for recovery. Directory removal must fail on remaining unknown contents. Retire both records only through the acknowledged-report boundary.

- Bind cleanup removal authority to the verified snapshot source inventory, never a later mutable observation. Compare the claimed namespace to that inventory and restore the source name without replacement on mismatch before any unlink.
- Snapshot walks compare the root's opened/named identity, mode, size and modification time around enumeration and copying, as for nested directories. A late root entry or retained-handle mutation cannot yield a complete inventory or removal authority.

- Snapshot creation/cleanup holds one Worker-wide cross-process publication gate through capacity admission and durable publication. Reject at 4,096 retained snapshots before staging; independent session locks cannot reserve the final global slot. A busy publication gate returns conflict without creating output or touching source.

- Snapshot copying shares one remaining byte/entry budget across workspace data and every independent Git store. Reserve bounded manifest/config-rewrite byte headroom before payload writes, count Git roots once, reserve an entry before recreating an absent Git config, and charge only new overlay directories. Reject excess files before creating them; growing files cannot write beyond their metadata reservation.

- Snapshot deletion reserves two inventory entries for its workspace/manifest wrappers beyond the 8,192-entry workspace bound; reject unexpected snapshot-root content.

- Reject partial-clone/promisor configuration and retained `.promisor` pack markers before creation and during inspection. Promisor-aware fsck cannot prove complete offline object closure.

- Failed snapshot capture retains recovery ownership when independent scratch removal is unconfirmed. Explicit recovery must remove the whole original staging tree before settling failure.

- On Windows retain the native file/directory symlink kind in the private inventory and recreate that type explicitly, including forward and dangling directory targets. Link-text equality alone cannot prove faithful restoration.
- Restored Git identity checks use the same offline read-only profile as snapshot inspection, including command-local Windows long-path support. Failure logging contains only closed phases, session/repository IDs and stable codes, never paths or native output.

- Every Worker-owned Git invocation enables Windows long paths per command, including initial preparation; never modify source Git configuration or depend on ambient settings. Native failure logs contain only ownership IDs, stable cause/code, exit status and closed read-only/offline flags, never argv, paths or native output.

- Permanent deletion validates reserved snapshots against the original session/machine/preparation and captures the stored workspace manifest before removal. Restored independent Git stays within its managed root; never run its removal against an original Local/source checkout.

- Workspace file/diff/private PR observations and preparation/recovery/storage/permanent deletion share a separate cross-process per-session gate through anchored handles and read-child cleanup. Keep execution leases independent so views remain usable during native runs. New read process indexes bind the original session in the version-2 namespace before launch; reconcile them before destructive work. Unknown/legacy unassigned ownership stays recovery-required. Include the session-bound namespace in deletion absence checks and reject new observations behind its deletion tombstone.

- Independent linked-worktree Git stores retain common branch reflogs and overlay only colliding files from the selected worktree administration, including its HEAD log. Reflog-only unpushed/reset history must remain recoverable without the original Git store.

- Only snapshot create/cleanup and restore may create scratch. Persist an external versioned operation/request/preparation/snapshot-bound claim with native root identity after exclusive directory creation and before copying. Direct cleanup, explicit recovery and permanent deletion must verify this proof through an anchored remover; missing, legacy or replaced staging stays protected. Preview never owns scratch. Keep claims in deletion absence inventories; later reappearing staging cannot be removed by generic Worker copy cleanup.

- Snapshot creation promotes its original staging claim with the exact manifest digest only after successful synchronized no-replace publication and post-rename content/native-root verification. Inspection, recovery and deletion require that original proof; matching manifests or foreign copied bytes cannot reconstruct it. Creation recovery additionally compares the complete original request digest. Missing proof retains the snapshot, source and recovery ownership.

- Removal journals retain an independent 64 MiB bound for repeated path transitions while immutable manifests/claims keep their 8 MiB bound. Before journal capacity is exhausted, atomically replace only settled history with the equivalent active prepared/renamed/directory-mode replay; preserve original header/root/intent identities and fail closed on malformed/canceled compaction. Published create and cleanup snapshots retain recovery-required ownership after any later failure.

- Source observation shares one 8,192-entry/8-GiB allowance across the complete workspace and all original Git administration/object inventories, before opening or hashing each admitted payload. Oversized observation cannot publish partial preview authority. Permanent deletion inventories every original removal claim `.json` and append journal `.pending`, including completed-proof absence replay.

- New snapshot capture/observation reserves bounded private rename spellings for every ancestor, complete snapshot/Git wrappers and the native removal-root path before admission. Retain the original 4,096-byte replay path bound; reject insufficient headroom before publication/removal. Inspect the original `.git` entry before Git resolves administrative paths; regular linked-worktree pointers are supported but symlink administration is unsupported.

- Source observation counts an independent Git store already covered by the complete managed-root inventory only once. Its bytes and changes remain bound to the root digest; external original stores still consume the shared allowance.

- Recovery retains the inspected snapshot logical source count. Persist that exact count in the original claim-bound removal intent before snapshot deletion; absence cannot reconstruct it from physical metadata size. Unpublished partial restore scratch cleanup uses the original external operation claim and native root identity, without requiring completed snapshot equality. Foreign or missing proof remains protected.

All snapshot namespace publication, storage inspection/restore/delete/recovery, retained inventory scans and permanent-session copy removal share the same cross-process gate after the session/observation locks. Hold it through native effects and final inventory accounting; contention fails before any effect.

- Claimed removal accepts only immutable directory permissions or the exact writable native mode authorized by an original durable directory-mode transition. Retain that transition through journal compaction, partial recovery and failure; check directory identity and permissions again before unlink. Preserve retained-writer permission changes as unresolved recovery.

- Recovery after the top-level removal rename may create a missing original claim only when its private namespace remains intact and matches the complete synchronized intent, and neither a claim nor its journal exists. Partial or fully absent namespaces require the preexisting original claim. Malformed or replaced claims stay uncertain.

- Storage recovery owns a finite 3 MiB request/4 MiB job exception until original preparation/manifest evidence can be represented by immutable reference. Strict decoding preserves unknown/duplicate/trailing/UTF-8 rejection and the original request 1 MiB bound. The allowance never bypasses contextual native ownership verification or expands ordinary storage inputs.

- Storage atomic publications in shared directories use immutable-target `.pending-<UUID>.json-<suffix>` names; claim-journal compaction uses `.pending-<UUID>.pending-<suffix>`. Permanent deletion and completed-proof replay share a bounded 65,536-entry-per-directory remnant inventory. Preserve unrelated owners; unknown legacy/malformed or non-private remnants block completion. Keep the shared security writer exception scoped to storage. Follow `docs/cmds-delidev-storage-contract.md`.

- Snapshot observation and removal inventory bind the original native root mode as well as all child metadata. Reject a managed root that differs from its fixed private writable mode before snapshot publication or live-root rename; a stale preview cannot grant authority over changed root permissions. Preserve explicit nested-directory mode-transition proofs independently.

- Published storage recovery uses the original snapshot publication claim and captured managed-directory identities with pinned source bytes/digest. Later mutable copy eligibility or external Git availability cannot revoke that completed copy; a live Cleanup source settles as preserved/failed. Replacement directories remain uncertain. Legacy snapshots retain their previous identity checks. Windows external Git stores on a different volume count toward the shared observation budget.

Removal journals use newline-framed records. Validate the complete prefix before atomically discarding an unterminated final append; malformed complete records remain uncertain and unchanged. Retain per-entry removed receipts through compaction. Recovery accepts an absent entry only with its own durable renamed/removal proof, and rejects reappearing settled entries. Legacy cleared records grant no missing-entry authority.

Compact settled removal proof to one original inventory path per entry, without generated private-path repetition. Admit these projection records only through validated atomic compaction, and validate membership in the unchanged original intent. Successful recovered cleanup must emit its original canonical source/preview digest.

- OpenCode General Chat Fork uses the closed `opencode-general-chat-v1` copy profile with an independent 8,192-entry/256 MiB bound. Preserve omitted legacy Codex profiles and their bounds; apply the chosen bound to initial inspection, actual copy and both final comparisons. Only independently owned sibling managed session roots may be relocated; no links, Git administration or special files may enter this copy.
