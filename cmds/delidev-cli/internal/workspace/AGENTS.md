# DeliDev Worker workspace ownership

Follow the root and parent instructions and docs/cmds-delidev-workspace-contract.md.

- Worker-local snapshots and cleanup follow docs/cmds-delidev-storage-contract.md. Preserve ordered all-repository manifests, commits/unpushed history, index/worktree state, ignored/untracked regular files, modes and symlinks without dereferencing links. Reject unsupported special files, external Git object dependencies and undeclared nested Git administration, including directories and filesystem case aliases.
- Verify and synchronize every recoverable copy before claiming any source removal. Cleanup never pushes, removes original Local checkouts or overwrites foreign destinations. Restore all repositories atomically and preserve exact execution-lease identity.
- Retain private bounded cancellation/recovery journals and source data on incomplete outcomes. Logical removed bytes and retained snapshot cost remain distinct from measured free capacity. Successful snapshot inspect/restore/delete reports the pinned source bytes and the session's post-action retained inventory; snapshot deletion never counts as live-source removal. Tests use isolated temporary repositories and injected faults.

- Failed unpublished restoration cleans only its original operation-owned staging independently of caller cancellation; unconfirmed scratch cleanup stays recovery-required.
- Restoration ownership requires the original operation/snapshot-bound publication proof synchronized after the successful no-replace rename. Pending comparison bindings, matching foreign bytes and missing scratch never authorize recovery, execution identity or restored-workspace deletion; missing publication proof remains uncertain without replay.

- Cleanup/deletion recovery requires the original synchronized removal intent plus a matching verified namespace claim when both namespace names are absent. Persist the claim after inventory validation and before unlinking; an intent alone or filesystem absence is never verified removal. Retire both records only through the acknowledged-report boundary.

- Bind cleanup removal authority to the verified snapshot source inventory, never a later mutable observation. Compare the claimed namespace to that inventory and restore the source name without replacement on mismatch before any unlink.
- Snapshot walks compare the root's opened/named identity, mode, size and modification time around enumeration and copying, as for nested directories. A late root entry or retained-handle mutation cannot yield a complete inventory or removal authority.

- Snapshot creation/cleanup holds one Worker-wide cross-process publication gate through capacity admission and durable publication. Reject at 4,096 retained snapshots before staging; independent session locks cannot reserve the final global slot. A busy publication gate returns conflict without creating output or touching source.

- Snapshot copying shares one remaining byte/entry budget across workspace data and every independent Git store. Reserve bounded manifest/config-rewrite byte headroom before payload writes, count Git roots once, reserve an entry before recreating an absent Git config, and charge only new overlay directories. Reject excess files before creating them; growing files cannot write beyond their metadata reservation.

- Snapshot deletion reserves two inventory entries for its workspace/manifest wrappers beyond the 8,192-entry workspace bound; reject unexpected snapshot-root content.

- Reject partial-clone/promisor configuration and retained `.promisor` pack markers before creation and during inspection. Promisor-aware fsck cannot prove complete offline object closure.

- Failed snapshot capture retains recovery ownership when independent scratch removal is unconfirmed. Explicit recovery must remove the whole original staging tree before settling failure.

- On Windows retain the native file/directory symlink kind in the private inventory and recreate that type explicitly, including forward and dangling directory targets. Link-text equality alone cannot prove faithful restoration.
- Restored Git identity checks use the same offline read-only profile as snapshot inspection, including command-local Windows long-path support. Failure logging contains only closed phases, session/repository IDs and stable codes, never paths or native output.

- Permanent deletion validates reserved snapshots against the original session/machine/preparation and captures the stored workspace manifest before removal. Restored independent Git stays within its managed root; never run its removal against an original Local/source checkout.
