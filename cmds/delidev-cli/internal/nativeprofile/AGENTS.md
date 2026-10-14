# cmds/delidev-cli/internal/nativeprofile working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/nativeprofile/` and its descendants.
- This package is the single owner and resolver of persistent native homes. Resolve every native launch path through it; never allocate a temporary or per-execution native home and never add a hidden temporary-home fallback.
- Keys use typed purposes and stable identifiers only. Never derive a profile from an execution, request or attempt identifier.
- Keep registry, owner record and receipt versions closed; reject incompatible versions without rewriting them.
- Log with closed reasons only; never log secrets, native contents or personal paths.
- Delete a profile only for explicit session, account or storage deletion, and never while it is referenced or locked.

## Owning contracts

- [DeliDev native harness adapter contract](../../../../docs/cmds-delidev-harness-contract.md#persistent-native-profiles)
- [DeliDev storage operations](../../../../docs/cmds-delidev-storage-contract.md#persistent-native-profile-storage)
- [DeliDev owned process contract](../../../../docs/cmds-delidev-process-contract.md#profile-retention-and-process-journals)
- [DeliDev native subscriptions](../../../../docs/cmds-delidev-subscription-contract.md#persistent-account-profiles)
