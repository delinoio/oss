# DevHud Support and Severity Contract

## Support triage

Support uses the authenticated administrator surface only: `AdminService` metadata-only users, usage counters, upload metadata, and audit events. Record the request correlation ID, exact account/upload identifier, expected state, safe reason, and next action. Never copy settings bodies, credentials, signed/public URLs, DOM, screenshots, Deck results, local paths, prompts, agent output, or issue bodies into tickets or logs.

For diagnostics, ask the user to preview the exact redacted payload first. Guests may export but cannot submit; blocked and deletion-pending accounts cannot submit. Crash evidence is limited to the typed error code, build/platform/architecture, applicable CEF/Tauri revisions, bounded summary/stack, correlation IDs, quota state, and service availability.

## Upload, deletion, and audit cases

Use `AdminService.QuarantineUpload` or `AdminService.DeleteUpload` only with a validated non-blank reason and expected-state compare-and-set. Quarantine/removal is metadata-first and must not expose image bytes or locators. Preserve operation leases and tombstones; a conflict or uncertain removal remains visible for retry.

Account deletion is recoverable through `AccountService.RestoreAccount` for 30 days. Restore clears deletion state only and never an administrator block. After the recovery window, `devhud-api-sweeper` owns irreversible server-side pseudonymization/purge, public-object replacement or CDN invalidation, and retention pruning. Device-local PATs and R2 credentials never enter the API; each client owns immediate secure-store cleanup and eventual profile reconciliation. Report server-side purge completion once the applicable database, R2, and tombstone boundaries are confirmed; track per-device secure-store cleanup separately as best-effort reconciliation and report it only as each device returns and confirms cleanup.

## High-severity response

For a suspected CEF vulnerability, credential exposure, bad public artifact, unsafe updater, data exposure, or cross-channel release failure:

1. Stop the affected manual workflow before its next external mutation and preserve the exact run, candidate, provenance, and redacted diagnostics. Cancelling the run can invoke the mutating `rollback_pre_store` cleanup path, which withdraws held store submissions and may roll back infrastructure; when maintainers need to freeze the release for investigation, withhold the next protected approval instead of cancelling.
2. Determine whether API, sweeper, stores (`apple`, `google-play`, `chrome-web-store`), GitHub Release, updater, or `/devhud` is public.
3. Before any store is public, follow the documented pre-store withdrawal and rollback boundary. After any store is public, automatic rollback is prohibited; use coordinated roll-forward or emergency withdrawal.
4. For CEF, follow `docs/apps-devhud-operations-contract.md`: update `apps/devhud/cef-pins.json`, matching `Cargo.lock` entries, the root and desktop Cargo manifests, and `apps/devhud/scripts/verify-pins.mjs`; complete the compatibility matrix, build a new signed candidate, and make an explicit all-ten-manifest updater decision.
5. Communicate through the approved human maintainer channel. This repository has no remote-alert service or kill switch; never invent one in a support response.
