# apps/delidev working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `apps/delidev/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.
- Open external HTTP(S) links only through the native `open_external_url` command or the closed Provider guidance, App information and GitHub openers so the device-local General/Local browser destinations apply; never navigate the webview, accept renderer-supplied executables, or route OAuth/subscription authorization through those destinations. See [Browser destinations for external links](../../docs/apps-delidev-desktop-contract.md#browser-destinations-for-external-links).

- `claude-usage-state.ts` mirrors the closed server document `subscription.claude_usage`; change it only together with the server section and keep `claude_usage_client_contract_test.go` passing. Follow the [usage tracking presentation](../../docs/apps-delidev-subscription-settings-contract.md#claude-usage-tracking-presentation).

## Owning contracts

- [DeliDev desktop client](../../docs/apps-delidev-desktop-contract.md)
- [DeliDev Diagnostics Presentation](../../docs/apps-delidev-diagnostics-contract.md)
- [DeliDev desktop localization](../../docs/apps-delidev-localization-contract.md)
- [DeliDev parallel browser QA](../../docs/apps-delidev-qa-contract.md)
- [DeliDev macOS status widget](../../docs/apps-delidev-widget-contract.md)
- [API account browser OAuth](../../docs/cmds-delidev-account-oauth-contract.md)
- [DeliDev account lifecycle](../../docs/cmds-delidev-accounts-contract.md)
- [DeliDev protected account browser](../../docs/cmds-delidev-browser-contract.md)
- [DeliDev provider and model catalog](../../docs/cmds-delidev-catalog-contract.md)
- [DeliDev native session compaction](../../docs/cmds-delidev-compaction-contract.md)
- [DeliDev direct execution startup](../../docs/cmds-delidev-execution-startup-contract.md)
- [DeliDev same-account native session forks](../../docs/cmds-delidev-forks-contract.md)
- [DeliDev native harness adapter contract](../../docs/cmds-delidev-harness-contract.md)
- [DeliDev GitHub Integration Profiles](../../docs/cmds-delidev-integrations-contract.md)
- [OpenCode Go subscription contract](../../docs/cmds-delidev-opencode-go-subscription-contract.md)
- [DeliDev native read-only Sidechat](../../docs/cmds-delidev-sidechat-contract.md)
- [Repository Workflow Contract](../../docs/repository-workflow-contract.md)
