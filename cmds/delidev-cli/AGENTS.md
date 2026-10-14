# cmds/delidev-cli working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Source ownership

The installed Tailscale adapter lives in `internal/tailscale`; approval and ingress business flows remain in `internal/server`. Read the [connections contract](../../docs/cmds-delidev-connections-contract.md#tailscale-connections) before changing either boundary.

## OpenCode General Chat Fork profiles

Profile `opencode-general-chat-v1` (System 26 / Worker 15) forks plain text and step history. Profile `opencode-general-chat-v2` (System 104 / Worker 69) additionally clones completed plain timed reasoning. The server selects v2 only for a reasoning source on a Worker that holds both pairs; the profile is an omitted-when-v1 enum so v1 bytes never change. Keep `internal/harness/opencode/fork_history.go`, `internal/server/session_fork_opencode.go` and `apps/delidev/src/opencode-fork-profile.ts` aligned with the [fork contract](../../docs/cmds-delidev-forks-contract.md).

## Owning contracts

- [DeliDev desktop client](../../docs/apps-delidev-desktop-contract.md)
- [DeliDev account lifecycle](../../docs/cmds-delidev-accounts-contract.md)
- [DeliDev protected account browser](../../docs/cmds-delidev-browser-contract.md)
- [DeliDev provider and model catalog](../../docs/cmds-delidev-catalog-contract.md)
- [DeliDev native session compaction](../../docs/cmds-delidev-compaction-contract.md)
- [DeliDev command, server, and Worker contract](../../docs/cmds-delidev-contract.md)
- [DeliDev direct execution startup](../../docs/cmds-delidev-execution-startup-contract.md)
- [DeliDev same-account native session forks](../../docs/cmds-delidev-forks-contract.md)
- [DeliDev native harness adapter contract](../../docs/cmds-delidev-harness-contract.md)
- [OpenCode Go subscription contract](../../docs/cmds-delidev-opencode-go-subscription-contract.md)
- [DeliDev native API relay contract](../../docs/cmds-delidev-proxy-contract.md)
- [DeliDev native read-only Sidechat](../../docs/cmds-delidev-sidechat-contract.md)
- [DeliDev ssh setup contract](../../docs/cmds-delidev-ssh-setup-contract.md)
- [DeliDev native subscriptions](../../docs/cmds-delidev-subscription-contract.md)
- [DeliDev updates contract](../../docs/cmds-delidev-updates-contract.md)
- [DeliDev current-user service contract](../../docs/cmds-delidev-user-services-contract.md)
- [DeliDev v1 Connect contract](../../docs/protos-delidev-v1-contract.md)
