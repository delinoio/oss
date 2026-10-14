# apps/delidev/src-tauri working instructions

- Follow the root instruction-update policy and the nearest parent instructions.
- This file covers `apps/delidev/src-tauri/` and its descendants unless a more specific instruction file applies.
- Read the owning contracts below before changing behavior, including affected cross-domain consumers.

## Development and validation

Follow the parent instructions and the owning contracts in `docs/`. These rules retain the original requirements; cross-domain changes must also read the affected owners' instructions.

- Browser destination dispatch (`browser_opener.rs`, `browser_discovery.rs`, `browser_preferences.rs`, `external_url.rs`) must stay argv-only, credential-free and revalidated from a fresh native inventory; `browser_opener::dispatch` (system default) is the only path OAuth authorization may use. An admitted `grok-device-authorization-open` dispatch owns a per-opening cancellation flag that Dispose, window close, stop and revocation exhaustion set; never pass the host-wide stop flag to it. Update the `device-browser-links` permission, build manifest and `permission_tests.rs` together with any command change.

## Owning contracts

- [DeliDev desktop client](../../../docs/apps-delidev-desktop-contract.md)
- [DeliDev desktop localization](../../../docs/apps-delidev-localization-contract.md)
- [API account browser OAuth](../../../docs/cmds-delidev-account-oauth-contract.md)
- [DeliDev protected account browser](../../../docs/cmds-delidev-browser-contract.md)
- [DeliDev command, server, and Worker contract](../../../docs/cmds-delidev-contract.md)
- [DeliDev protected credential storage](../../../docs/cmds-delidev-credentials-contract.md)
- [DeliDev Session Files and Git Comparisons](../../../docs/cmds-delidev-files-contract.md)
- [DeliDev image input contract](../../../docs/cmds-delidev-image-input-contract.md)
- [DeliDev ssh setup contract](../../../docs/cmds-delidev-ssh-setup-contract.md)
- [DeliDev updates contract](../../../docs/cmds-delidev-updates-contract.md)
