# cmds/delidev-cli/internal/server working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/internal/server/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Owning contracts

- [Tailscale connections and ingress](../../../../docs/cmds-delidev-connections-contract.md#tailscale-connections)

- [DeliDev desktop client](../../../../docs/apps-delidev-desktop-contract.md)
- [API account browser OAuth](../../../../docs/cmds-delidev-account-oauth-contract.md)
- [DeliDev account lifecycle](../../../../docs/cmds-delidev-accounts-contract.md)
- [DeliDev protected account browser](../../../../docs/cmds-delidev-browser-contract.md)
- [DeliDev provider and model catalog](../../../../docs/cmds-delidev-catalog-contract.md)
- [DeliDev Claude native context and manual compaction contract](../../../../docs/cmds-delidev-claude-compaction-contract.md)
- [DeliDev native session compaction](../../../../docs/cmds-delidev-compaction-contract.md)
- [DeliDev command, server, and Worker contract](../../../../docs/cmds-delidev-contract.md)
- [DeliDev protected credential storage](../../../../docs/cmds-delidev-credentials-contract.md)
- [DeliDev Read-Only Diagnostics](../../../../docs/cmds-delidev-diagnostics-contract.md)
- [DeliDev direct execution startup](../../../../docs/cmds-delidev-execution-startup-contract.md)
- [DeliDev same-account native session forks](../../../../docs/cmds-delidev-forks-contract.md)
- [DeliDev native harness adapter contract](../../../../docs/cmds-delidev-harness-contract.md)
- [DeliDev image input contract](../../../../docs/cmds-delidev-image-input-contract.md)
- [DeliDev retained inbox contract](../../../../docs/cmds-delidev-inbox-contract.md)
- [DeliDev GitHub Integration Profiles](../../../../docs/cmds-delidev-integrations-contract.md)
- [DeliDev native Codex model observations](../../../../docs/cmds-delidev-native-models-contract.md)
- [DeliDev explicit outbound network contract](../../../../docs/cmds-delidev-network-contract.md)
- [OpenCode Go subscription contract](../../../../docs/cmds-delidev-opencode-go-subscription-contract.md)
- [DeliDev provider inspection](../../../../docs/cmds-delidev-providers-contract.md)
- [DeliDev native API relay contract](../../../../docs/cmds-delidev-proxy-contract.md)
- [DeliDev schedules and occurrence contract](../../../../docs/cmds-delidev-schedules-contract.md)
- [DeliDev automatic session titles](../../../../docs/cmds-delidev-session-titles-contract.md)
- [DeliDev session acceptance and input queue contract](../../../../docs/cmds-delidev-sessions-contract.md)
- [DeliDev native read-only Sidechat](../../../../docs/cmds-delidev-sidechat-contract.md)
- [DeliDev storage operations](../../../../docs/cmds-delidev-storage-contract.md)
- [DeliDev native subagent observations](../../../../docs/cmds-delidev-subagents-contract.md)
- [DeliDev native subscriptions](../../../../docs/cmds-delidev-subscription-contract.md)
- [DeliDev Session Terminals Contract](../../../../docs/cmds-delidev-terminals-contract.md)
- [DeliDev native usage ledger](../../../../docs/cmds-delidev-usage-contract.md)
- [DeliDev current-user service contract](../../../../docs/cmds-delidev-user-services-contract.md)
- [DeliDev Worker workspace contract](../../../../docs/cmds-delidev-workspace-contract.md)

## Persistent native profiles

- Resolve every native home through `internal/nativeprofile`; do not allocate per-execution or temporary native homes. Keep the fresh `runtimes/<id>` or `title-runtimes/<job>` directory only as the operation journal.
- Explicit account deletion removes all server-owned profiles of the account before it is acknowledged (`deleteAccountNativeProfiles` in `resources.go`).
- Server-owned subscription operations (login, refresh, observation, quota/credit and Claude setup-token) retain account-owned credential files and vault generations after a joined operation; only the published generation grants authority. Remove credentials only on explicit logout or account deletion (use `removeAccountLoginReferences`, which has no reference-count cap). Worker-owned cleanup keeps its existing lifecycle. Retain history, caches and native-created helper links, and delete profiles only through explicit session, account or storage deletion.
- Follow the [persistent native profiles contract](../../../../docs/cmds-delidev-harness-contract.md#persistent-native-profiles).
