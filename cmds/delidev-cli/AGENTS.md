# cmds/delidev-cli working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `cmds/delidev-cli/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Source ownership

The installed Tailscale adapter lives in `internal/tailscale`; approval and ingress business flows remain in `internal/server`. Read the [connections contract](../../docs/cmds-delidev-connections-contract.md#tailscale-connections) before changing either boundary.

The server-owned Claude usage tracking lifecycle is owned by `internal/server` (`claude_usage_*.go`), its exclusive profile adapter by `internal/harness/claude` (`usage_*.go`) and the `usage` purpose of `internal/nativeprofile`. No other package reads native Claude credential storage, and its logs keep the closed vocabulary enforced by `claude_usage_logs_test.go`. Read the [subscription contract](../../docs/cmds-delidev-subscription-contract.md#server-owned-claude-usage-tracking) before changing any of these boundaries.

## Native OpenCode tests

Opt-in native OpenCode tests must obtain the binary and its observed version only through `nativetest.NativeOpenCodeExecutable(t)` (`internal/nativetest`); never read `DELIDEV_NATIVE_OPENCODE_EXECUTABLE` directly and never pin real-binary probe configs to `opencode.SupportedVersion`. Follow the [native OpenCode testing runbook](../../docs/cmds-delidev-opencode-native-testing.md).

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
- [DeliDev native OpenCode testing runbook](../../docs/cmds-delidev-opencode-native-testing.md)
- [OpenCode Go subscription contract](../../docs/cmds-delidev-opencode-go-subscription-contract.md)
- [DeliDev native API relay contract](../../docs/cmds-delidev-proxy-contract.md)
- [DeliDev native read-only Sidechat](../../docs/cmds-delidev-sidechat-contract.md)
- [DeliDev ssh setup contract](../../docs/cmds-delidev-ssh-setup-contract.md)
- [DeliDev native subscriptions](../../docs/cmds-delidev-subscription-contract.md)
- [DeliDev updates contract](../../docs/cmds-delidev-updates-contract.md)
- [DeliDev current-user service contract](../../docs/cmds-delidev-user-services-contract.md)
- [DeliDev v1 Connect contract](../../docs/protos-delidev-v1-contract.md)
