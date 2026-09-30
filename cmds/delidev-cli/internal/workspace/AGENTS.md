# DeliDev Worker workspace ownership

Follow the root and parent instructions and docs/cmds-delidev-workspace-contract.md.

- Worker-local snapshots and cleanup follow docs/cmds-delidev-storage-contract.md. Preserve ordered all-repository manifests, commits/unpushed history, index/worktree state, ignored/untracked regular files, modes and symlinks without dereferencing links. Reject unsupported special files, external Git object dependencies and undeclared nested Git administration, including directories.
- Verify and synchronize every recoverable copy before claiming any source removal. Cleanup never pushes, removes original Local checkouts or overwrites foreign destinations. Restore all repositories atomically and preserve exact execution-lease identity.
- Retain private bounded cancellation/recovery journals and source data on incomplete outcomes. Logical removed bytes and retained snapshot cost remain distinct from measured free capacity. Tests use isolated temporary repositories and injected faults.

- Failed unpublished restoration cleans only its original operation-owned staging independently of caller cancellation; unconfirmed scratch cleanup stays recovery-required.
