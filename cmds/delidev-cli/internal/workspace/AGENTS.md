# DeliDev Worker workspace ownership

Follow the root and parent instructions and docs/cmds-delidev-workspace-contract.md.

- Worker-local snapshots and cleanup follow docs/cmds-delidev-storage-contract.md. Preserve ordered all-repository manifests, commits/unpushed history, index/worktree state, ignored/untracked regular files, modes and symlinks without dereferencing links. Reject unsupported special files, external Git object dependencies and undeclared nested Git administration, including directories and filesystem case aliases.
- Verify and synchronize every recoverable copy before claiming any source removal. Cleanup never pushes, removes original Local checkouts or overwrites foreign destinations. Restore all repositories atomically and preserve exact execution-lease identity.
- Retain private bounded cancellation/recovery journals and source data on incomplete outcomes. Logical removed bytes and retained snapshot cost remain distinct from measured free capacity. Tests use isolated temporary repositories and injected faults.

- Failed unpublished restoration cleans only its original operation-owned staging independently of caller cancellation; unconfirmed scratch cleanup stays recovery-required.
- Restoration ownership requires the original operation/snapshot-bound publication proof synchronized after the successful no-replace rename. Pending comparison bindings, matching foreign bytes and missing scratch never authorize recovery, execution identity or restored-workspace deletion; missing publication proof remains uncertain without replay.

- Cleanup/deletion recovery requires the original synchronized removal intent plus a matching verified namespace claim when both namespace names are absent. Persist the claim after inventory validation and before unlinking; an intent alone or filesystem absence is never verified removal. Retire both records only through the acknowledged-report boundary.

- Bind cleanup removal authority to the verified snapshot source inventory, never a later mutable observation. Compare the claimed namespace to that inventory and restore the source name without replacement on mismatch before any unlink.

- Snapshot deletion reserves two inventory entries for its workspace/manifest wrappers beyond the 8,192-entry workspace bound; reject unexpected snapshot-root content.

- Reject partial-clone/promisor configuration and retained `.promisor` pack markers before creation and during inspection. Promisor-aware fsck cannot prove complete offline object closure.

- Failed snapshot capture retains recovery ownership when independent scratch removal is unconfirmed. Explicit recovery must remove the whole original staging tree before settling failure.

- On Windows retain the native file/directory symlink kind in the private inventory and recreate that type explicitly, including forward and dangling directory targets. Link-text equality alone cannot prove faithful restoration.

- Permanent deletion validates reserved snapshots against the original session/machine/preparation and captures the stored workspace manifest before removal. Restored independent Git stays within its managed root; never run its removal against an original Local/source checkout.
