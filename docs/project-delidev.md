# Project: DeliDev

## Goal
Run personal AI sessions across projects, accounts, native harnesses, and execution machines with durable single-user ownership. Issue #964 remains normative, with the explicit owner startup/presentation amendment in #1137; implementation and real-environment evidence are distinct.

Issue #1137 makes a fresh main desktop launch sufficient to start/reuse a compatible ordinary local runtime and verify the authenticated product connection. Native-service scope admission remains Go-owned; same-process Stop, renderer lifecycle, saved-window authority and detached server/Worker/session lifetime stay independent. Routine startup/sidebar/tray use product wording; lifecycle, registration and saved-connection controls live in persistent Connection & diagnostics. [Desktop contract](apps-delidev-desktop-contract.md) and [issue evidence](evidence/delidev/issue-1137/launch-default.md) define implementation and actual platform limits.

## Project ID
`delidev`; the Go component is `delidev-cli` and its executable is `delidev`.

## Domain Ownership Map
- `apps/delidev`: desktop presentation and native host.
- `cmds/delidev-cli`: Go CLI, server, Worker, storage and native adapters.
- `protos/delidev/v1`: versioned Connect schema; `protos/gen/go/delidev/v1`: generated Go bindings.
- `packages/delidev-api-client`: generated TypeScript client and bounded transport/synchronization helpers.

New schedule creation adds frequency presets and a creation-only three-section layout under the [desktop contract](apps-delidev-desktop-contract.md#new-schedule-creation-issue-1152), while strict schedule definitions and server recurrence authority remain unchanged.

## Domain Contract Documents
- [macOS status widget](apps-delidev-widget-contract.md)
- [Native package verification](apps-delidev-packaging-contract.md)
- [Storage operations](cmds-delidev-storage-contract.md)
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Complete issue #964 requirements](cmds-delidev-requirements.md)
- [Protocol contract](protos-delidev-v1-contract.md)
- [TypeScript client contract](packages-delidev-api-client-contract.md)
- [Desktop client contract](apps-delidev-desktop-contract.md), including [Agent Worker core/optional presentation](apps-delidev-desktop-contract.md#agent-worker-core-and-optional-presentation) and its [issue #1158 evidence](evidence/delidev/issue-1158/agent-settings.md)
- [AI Subscription settings](apps-delidev-subscription-settings-contract.md)
- [Worker workspace contract](cmds-delidev-workspace-contract.md)
- [Session file explorer and Git comparisons](cmds-delidev-files-contract.md)
- [Automatic session titles](cmds-delidev-session-titles-contract.md)
- [Portable configuration](cmds-delidev-configuration-transfer-contract.md)
- [Owned process contract](cmds-delidev-process-contract.md)
- [Optional current-user services](cmds-delidev-user-services-contract.md)
- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Read-only diagnostics](cmds-delidev-diagnostics-contract.md)
- [Diagnostics presentation](apps-delidev-diagnostics-contract.md)
- [Saved client connections](cmds-delidev-connections-contract.md)
- [Session development-server forwarding](cmds-delidev-forwarding-contract.md)
- [GitHub integration profiles](cmds-delidev-integrations-contract.md)
- [Account lifecycle and AI API Keys presentation](cmds-delidev-accounts-contract.md)
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Provider and model catalog](cmds-delidev-catalog-contract.md)
- [API provider activation](cmds-delidev-provider-activation-contract.md)
- [Native API relay](cmds-delidev-proxy-contract.md)
- [Session acceptance and input queue](cmds-delidev-sessions-contract.md)
- [Retained inbox and read-state contract](cmds-delidev-inbox-contract.md)
- [Retained conversation search](cmds-delidev-search-contract.md)
- [Retained activity](cmds-delidev-activity-contract.md)
- [Exact native response usage](cmds-delidev-usage-contract.md)
- [Schedules and durable occurrences](cmds-delidev-schedules-contract.md)
- [Implementation and evidence ledger](cmds-delidev-evidence.md)

## Cross-Domain Invariants
- Go owns business logic; clients use authenticated Connect and preserve exact request/revision identities.
- Local and remote operation preserve original native ownership, explicit authorization, credential isolation and uncertainty.
- Real native/account/platform evidence remains distinct from deterministic fixtures, cross-compilation and packaging.
- Keep complete issue #964 requirements and unresolved acceptance items visible.
- PR activity preserves immutable source/version/actor metadata across Go, generated clients, CLI and desktop. Attempt success cannot establish verified handling; only a dedicated original verification source can project that outcome.

## Change Policy
Update the owning domain contract when behavior changes. Update this index only for ownership, its domain catalog or cross-domain invariants. Record each implementation/validation increment in its own `docs/evidence/delidev/issue-<number>/` file. A validation-only increment does not require editing this index or an AGENTS file.

## References
- https://github.com/delinoio/oss/issues/964
- [Repository defaults](repository-defaults.md)
- [Project template](project-template.md)
- [Structure and compatibility](cmds-delidev-structure-contract.md)
- [Relocation inventory](evidence/delidev/pr-conflict-structure/document-relocations.json)
