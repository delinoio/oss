# DeliDev Activity retirement

## Scope
Issue #1977 retires the Activity product. This contract owns the authenticated compatibility handler in `cmds/delidev-cli/internal/server/activity.go`, legacy metadata validators in domain `pr_activity.go`, dependent erasure in store `pr_activity.go` and `session_deletion_remediation.go`, and the unchanged `ActivityService` declarations in `protos/delidev/v1/activity.proto`.

## Runtime and Language
Go in the pinned root module, SQLite and authenticated Connect. No database migration, new allocation, historical backfill or destructive purge is required.

## Users and Operators
The desktop has no Activity destination, context pane, source disclosure, shortcut or command-palette entry. Its main rail contains Sessions, Pull requests, Usage and Schedules. Inbox and Search remain header actions; Settings, subscription controls and compact navigation retain their independent lifetimes. Independent session status, native observations, Inbox, schedule occurrence history, Token Usage and PR handling remain supported.

The CLI has no `activity list` command or help entry. A retired or unknown command is rejected before server connection, credential input or local server state creation.

## Interfaces and Contracts
`ActivityService.ListActivity` remains registered for old generated clients. Preserve all original RPC, message, field and enum numbers and generated compatibility exports. Authenticated owners and current paired clients receive typed `UNSUPPORTED` with Connect `Unimplemented`, safe retirement guidance and correlation information. Filters, malformed IDs and old cursors do not cause source reads or change this retirement outcome. Missing, invalid or revoked credentials and Worker actors retain their authentication/permission refusal. The handler does not access storage, credentials, Worker admission, native execution, GitHub or providers.

Remove the live chronological projection and all new PR Activity transition and dedicated handling-verification snapshot publication. Canonical problem collection, dismissal, alias deduplication, exact mutation receipts, remediation attempt state/indexes and handling remain transactional and independently readable. Successful attempts and provider resolution cannot establish verified handling. The manual verifier still requires the original assignment, native completion, independent cleanup and exact verified-push proof before retaining canonical handled versions; no Activity record is needed for that authoritative result.

## Legacy Storage and Cleanup
Previously stored `pull-request-activity` and `pull-request-handling-verification` resources remain private legacy metadata. Keep their closed validators, original UUIDs/revisions, stable remote repository/PR identities, original problem/content-version references, actor namespace/device/request, typed mode/state and retention time. Preserve their metadata-only content limits: no copied prompt, output, instruction, feedback/comment bodies, credentials or native conversation identifiers. A private original verification commitment remains private; an old snapshot does not grant execution or new verification authority.

Original shared problem observation/dismissal/verification records have no session owner. Legacy attempt snapshots retain their original source attempt and, when bound, session/project scope. Permanent session erasure removes both bound snapshots and earlier unbound reservations through each exact original attempt source. Redact validated shared coordination metadata, indexes and associated receipts while preserving unrelated shared PR evidence, original reservation provenance and lifetime counters. Never retire unrelated terminal, native, execution or runtime ownership as a consequence of removing Activity.

Managed backup restore/quarantine and tombstone replay retain the same dependent cleanup before exposing a restored database. An old backup cannot resurrect deleted session-owned snapshots or reservations. Preserve original temporary-image/journal ownership and independent cleanup uncertainty under the storage contract. No wholesale deletion of legacy resources occurs at startup or upgrade.

## Security and Logging
Authorization precedes the compatibility outcome. Preserve authenticated correlation and bounded safe guidance without source documents, raw native content, user state or secrets. Activity retirement grants no account, native or execution support and cannot approve, dispatch, resume or confirm cleanup.

## Build and Test
Run `go test -race -p 1 ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, relevant uncached Go fixtures, frontend `pnpm test` and localization checks. Preserve Buf format/lint/compatibility and generated binding freshness. Fixtures cover owner/client retirement, malformed filters/legacy cursors, unauthenticated/revoked/Worker refusal, nil product dependencies, no outbound/native calls, no CLI connection/credential side effects, retained navigation, canonical PR transaction/replay/proof behavior, and legacy session deletion/managed restore. Fixture/build evidence does not establish native/account/platform acceptance.

## Dependencies and Change Triggers
Preserve independent execution, Inbox, schedule, usage, PR, deletion and backup ownership. Keep declarations/generated clients compilable, synchronize CLI help and relevant AGENTS/contracts, and record source revision, checks and unresolved limits in PR/issue/CI records rather than repository evidence documents.

## References
- [DeliDev project](project-delidev.md)
- [Desktop contract](apps-delidev-desktop-contract.md)
- [Integrations contract](cmds-delidev-integrations-contract.md)
- [Storage contract](cmds-delidev-storage-contract.md)
- [Protocol contract](protos-delidev-v1-contract.md)
