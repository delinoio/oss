# cmds/delidev-cli/internal/nativeprofile working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/nativeprofile/` and its descendants.
- This package is the single owner and resolver of persistent native homes. Resolve every native launch path through it; never allocate a temporary or per-execution native home and never add a hidden temporary-home fallback.
- Keys use typed purposes and stable identifiers only. Never derive a profile from an execution, request or attempt identifier.
- Keep registry, owner record and receipt versions closed; reject incompatible versions without rewriting them.
- Log with closed reasons only; never log secrets, native contents or personal paths.
- Delete a profile only for explicit session, account or storage deletion, and never while it is referenced or locked.
- A deletion scope succeeds only when no matching profile remains: any locked, referenced or unremovable match must return an error even when other matches were already removed.
- Creation is all-or-nothing: close the lease on every failure and roll back a new directory unless its registry entry is proven durable.
- Registry load validates every entry's owner kind, server, machine and (for legacy entries) the normalized local path below the owner root; reject the whole root otherwise.
- The `usage` purpose is the exclusive Claude Code usage-tracking profile of one server-token account. Only the Claude usage adapter may read or remove its native credential storage; never enumerate, adopt or share it with execution, personal or legacy profiles.
- Write credential files through `subscription.WriteCredentialFile` so interrupted-write temporaries stay attributable and are retired with the credential files.

## Owning contracts

- [DeliDev native harness adapter contract](../../../../docs/cmds-delidev-harness-contract.md#persistent-native-profiles)
- [DeliDev storage operations](../../../../docs/cmds-delidev-storage-contract.md#persistent-native-profile-storage)
- [DeliDev owned process contract](../../../../docs/cmds-delidev-process-contract.md#profile-retention-and-process-journals)
- [DeliDev native subscriptions](../../../../docs/cmds-delidev-subscription-contract.md#persistent-account-profiles)
