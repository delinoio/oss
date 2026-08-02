# protos-devhud-realqa-api-contract

## Scope

- Project/component: `devhud` / `realqa-api`
- Canonical source path: `protos/devhud-realqa/v1`
- Contract identity: `devhud.realqa.v1`
- Status: implemented source contract and private workspace package for issue #757, consumed by the inactive `servers/devhud-realqa` preset/tracker/auth/deletion, online submission/live-transfer/initial-storage authorization, and recurring storage/rebind/grace/terminal-cleanup implementation; no deployed API, public client publication, or production activation is claimed.

## Runtime and Language

- Versioned Protobuf is authoritative.
- Generate reproducible Connect-compatible Go and protobuf-es v2 TypeScript artifacts under `protos/devhud-realqa/gen/go` and `protos/devhud-realqa/gen/ts`; generated files are derived and never a second contract.
- The private workspace TypeScript package is `@delinoio/devhud-realqa-connect`. Its root export and versioned `./devhud-realqa/v1/*` subpath exports are repository-internal and must not be published.
- Shared repository-issue-definition messages live in `common.proto`, so the generated tracker descriptor depends only on common messages and does not pull preset or submission service descriptors into tracker-only consumers. The unchanged v1 symbols remain compatible in the supported generated runtimes through Go's package-level symbols and the TypeScript preset-subpath facade; the scoped Buf relocation exceptions are paired with contract tests that retain every other preset message and enum.
- Preserve released v1 fields additively. Breaking changes require `devhud.realqa.v2` and synchronized consumer migration docs.

## Services and RPCs

- `RealQAPresetService`: `ListPresets`, `GetPreset`, `CreatePreset`, `UpdatePreset`, `DeletePreset`, `DeleteFeatureData`.
- `RealQATrackerService`: `GetGitHubConnection`, `StartGitHubConnection`, `ListGitHubInstallations`, `DisconnectGitHubConnection`, `ListRepositories`, `GetRepositoryIssueSchema`.
- `RealQASubmissionService`: `ListSubmissions`, `CreateSubmission`, `CreateImageUpload`, `FinalizeImageUpload`, `SubmitIssue`, `GetSubmission`, `RebindSubmissionStorageAuthorization`, `DeleteImage`, `DeleteSubmissionAssets`.

No additional v1 service or RPC is implied. GitHub callback/webhook, same-origin signed image PUT, and public image GET handlers are narrow HTTP/server boundaries.

## Wire Contract

- IDs and idempotency keys are UUID v7 wrappers. Owner scope, capture mode, selector mode, tracker kind, submission state, upload state, failure class, asset state, and stable error reason are closed enums.
- The only v1 tracker is GitHub.com. The internal tracker abstraction is not exposed as a plugin protocol.
- Presets carry owner/billing scope, capture/pointer/selector defaults, destination/repository, template/form choice, supported provider extensions, ordered safe process/title URL mappings, shortcut, and revision.
- `CreatePreset` carries a stable client-generated UUID v7 idempotency key scoped to the authenticated subject and operation. An exact replay returns the original preset and revision without consuming another preset-limit slot; reuse with changed creation input fails with the typed idempotency-conflict reason.
- Lists use opaque cursors. Enforce 50 personal presets, 250 organization presets, and 20 active device shortcuts.
- Archived repositories remain listable with their provider issue setting, but `caller_can_submit` is false.
- Repository issue schemas preserve each template/form's GitHub issue type, each Issue Form input/textarea prefilled value, each textarea code-block render language, and whether a dropdown accepts multiple selections so clients can render and validate provider-compatible answers.
- Synchronized writes carry expected revision and return a new revision; typed conflicts support reload/compare/reapply.
- Submission creation carries the stable local idempotency key. When accepted declared bytes produce a positive transfer reservation, the response returns its expiry plus the server-derived upload deadline; the deadline is no later than 23 hours after reservation creation and reserves at least the final hour before `expires_at` for verification and commit/release. Upload messages carry that same deadline and bounded metadata needed for a short-lived signed PUT at exactly `https://assets.realqa.deli.dev`; no signed PUT may outlive the deadline, and messages never carry an R2 S3 endpoint or raw image bytes through Connect. Zero accepted bytes return no transfer reservation/deadline and cannot create an image upload. The first `SubmitIssue` attempt must arrive by the upload deadline with a fresh forwarded delibase bearer; an exact authenticated replay of that accepted attempt may resume through `expires_at`. Its server-side per-submission attempt uses stable downstream transfer-finalization and initial storage-authorization-creation idempotency keys; exact authenticated retries recover ambiguous results before provider creation. If no authenticated transfer-finalization attempt starts, the live reservation expires and staging cleanup performs no `CommitUsage` or `ReleaseUsage`; an unresolved storage-authorization attempt remains retryable and cannot be treated as proof that no grant exists.
- `StartGitHubConnection` returns an ephemeral GitHub.com authorization target for the scoped native system-browser action. It is never a general navigation field: clients and native hosts must apply the exact authorization host/path validation in the app contract and must not log, cache, persist, or reuse its query data.
- `ListSubmissions` uses opaque cursor pagination and server-side owner authorization. Each result exposes only the retained submission UUID, asset UUID/state summaries, bounded timestamps, and minimum provider issue ID/URL needed to identify records available to `GetSubmission`, `DeleteImage`, or `DeleteSubmissionAssets`; it never returns submitted title/body/URL/DOM data, screenshot bytes, object keys, or a public asset index.
- Submission/detail summaries expose typed storage recovery without credentials or billing PII: exact authorization state, authorization/payment/overage/provider/security reason, applicable rebind/payment/revoke actions, notification-delivery state, and the fixed grace start/expiry timestamps. Delivery through an authorized read marks the durable notification as notified.
- The API represents no explicit image-count limit, while errors enforce 25 MiB/encoded image, 250 MiB/session, 100 megapixels/decoded image, and 60,000 UTF-8 bytes/final issue body.
- Final submission carries explicit public-image confirmation. Reconciliation state represents ambiguous GitHub responses and the hidden marker without exposing the marker as a user-editable identity. The server does not retain the forwarded bearer: an interrupted transfer or initial-authorization attempt can resume only in a fresh authenticated `SubmitIssue` request before its applicable deadline, while cleanup retains unresolved authorization-attempt state until the exact grant can be recovered and closed.
- `RebindSubmissionStorageAuthorization` is owner-authorized and carries the submission UUID, expected current authorization UUID and mapping revision, replacement payer organization/team selection, and a stable UUID v7 idempotency key. It is accepted only for a submission in storage-billing recovery. Through the caller's memory-only forwarded delibase bearer, the server loads and validates the exact current submission-bound authorization; it revokes it only when `ACTIVE`, may proceed without another transition when it is already `REVOKED` or `ACCESS_LOST`, and rejects `RESOURCE_DELETED`, `OWNER_DELETED`, or any binding substitution. It then creates and validates the replacement `REALQA_STORAGE` authorization and atomically compare-and-swaps the submission mapping. Exact replays return the same replacement authorization and mapping revision; changed-input reuse or a stale expected mapping fails with a typed conflict. A replacement that cannot be installed after the submission leaves grace or payer access is lost is replayed and closed through its stable key; that cleanup terminalizes the attempt so a distinct payer-authorized idempotency key can retry. Messages never carry a client secret or make client-supplied service, meter, purpose, period, maximum, authorization status, or access authoritative.
- Deletion messages distinguish image/range deletion, submission-asset deletion, and feature deletion. `DeleteFeatureData` carries a closed trigger union. Owner-request mode carries personal or organization owner scope plus an idempotency key; a personal caller may delete only their own data, while organization deletion requires an Owner. Delibase-lifecycle mode carries only an account or organization target UUID plus the immutable delibase deletion-job UUID and requires the exact RealQA-scoped delibase M2M identity. The deletion-job UUID is the replay identity, DeliDev and ordinary feature callers cannot select lifecycle mode, and absent feature data succeeds idempotently.
- The first accepted feature-deletion request blocks new access and asynchronously removes the scope's presets, submissions, assets, tracker connection and installation bindings, envelope-encrypted provider credentials, and related callback/webhook state; exact replays return the same deletion-job result. Responses preserve the stable removed-placeholder URL contract and required pseudonymized financial/security records.
- Client-provided role, GitHub permission, billing authorization/price, upload verification, provider success, and asset state are never authoritative.

## Authentication, Privacy, and Errors

- Human RPCs use `Authorization: Bearer <realqa-audience-user-access-token>` plus the exact dedicated `x-delibase-forwarded-user-token: <delibase-audience-user-access-token>` Connect metadata. Both are memory-only, their subjects must match, and neither may appear in a protobuf message. The forwarded bearer for `SubmitIssue` requires both `delibase:usage:execute` for live transfer finalization and `delibase:billing:write` for initial storage-authorization creation. Only `DeleteFeatureData` in delibase-lifecycle mode instead requires the exact RealQA-scoped delibase M2M identity and rejects the forwarded-user header; that identity is rejected by every other procedure. The package-local `AUTHENTICATION.md` owns the complete scope table. Generated clients keep all credentials out of messages, logs, errors, caches, persistence, and diagnostics.
- The DevHud generated client runs through the private native Connect transport's closed procedure/origin mapping rather than browser fetch. The separately scoped native uploader validates the exact signed asset-origin PUT capability returned by `CreateImageUpload`. Neither path grants an arbitrary URL, header, method, redirect, or `http://tauri.localhost` CORS access.
- Errors distinguish authentication/reauthentication, authorization, stale revision or storage-authorization mapping, idempotency conflict, disconnected/unsupported host, provider permission/schema/validation, body/image/session limits, malformed/unsupported/decompression-bomb input, upload deadline/expiry or verification, ambiguous reconciliation, rate/concurrency limit, billing reservation/background-authorization/rebind/grace, asset removed, and retention state.
- Messages must not carry GitHub tokens, webhook/R2 secrets, raw authorization headers, production secrets, raw originals, arbitrary HTML/page text, or remotely supplied UI/code.
- Submitted title/body/URL/DOM content must not remain in ordinary server messages/storage after reconciliation beyond the response needed by the active operation.

## Build and Test

Canonical checks are:

- `pnpm generate:proto`;
- `pnpm check:proto`;
- `pnpm --dir protos/devhud-realqa generate:proto`;
- `pnpm --dir protos/devhud-realqa check:proto`;
- `go test ./protos/devhud-realqa/...`;
- `go vet ./protos/devhud-realqa/...`;
- `pnpm --filter @delinoio/devhud-realqa-connect typecheck`.
- `pnpm ci:realqa:proto`, the root Turborepo wrapper for the component-local compatibility, reproducibility, Go test/vet, and TypeScript checks.

The workspace package owns `buf.gen.yaml`, component-local generation/check scripts, `gen/go`, `gen/ts`, and `devhud.realqa.v1.binpb`. Generation scopes every Buf operation to `protos/devhud-realqa/v1`, cleans and writes only those owned outputs, lints, checks only the immutable RealQA descriptor baseline, generates Go/TypeScript artifacts, builds the TypeScript package, and rejects nondeterminism or a component-local generated diff. Because component ownership requires `protos/devhud-realqa/v1` while the public package is `devhud.realqa.v1`, the shared Buf policy grants only that directory a `PACKAGE_DIRECTORY_MATCH` exception; every other `STANDARD` rule remains enabled. The fixed-order root aggregate and `proto-contracts` CI job remain unchanged; the dedicated change-scoped `realqa-proto` job additionally resolves only the immutable RealQA descriptor from the pull-request base or prior push commit and runs the package task. Both jobs are aggregated by `CI Result`. CI fails on compatibility, service/RPC/enum drift, stale artifacts, sensitive metadata leakage, cross-component writes, or missing cross-consumer generation.

Checks do not publish the TypeScript package, deploy either RealQA origin, create R2 infrastructure, register the GitHub App, publish the extension, activate a catalog entry, or publish a server image.

## Dependencies and Change Triggers

- Owned by `devhud`; consumed by `servers/devhud-realqa`, authenticated RealQA code under `apps/devhud`, DeliDev only for the six `RealQATrackerService` settings RPCs in its existing account/organization settings sections, and `servers/delibase` only for service-authenticated `DeleteFeatureData` lifecycle delivery. DeliDev must not consume preset/submission/deletion services or add a RealQA feature route.
- Recurring storage settlement depends on the separately synchronized delibase background-usage contract implemented by issue #756. RealQA's live transfer, initial authorization client, M2M authorized-usage worker, rebind, and authorization closure are implemented; catalog/client activation remains future work. The live forwarded-token `ReserveUsage`/`CommitUsage`/`ReleaseUsage` RPCs are not a substitute for recurring settlement.
- Update this document, [project-devhud](project-devhud.md), [servers-devhud-realqa-foundation](servers-devhud-realqa-foundation.md), [apps-devhud-foundation](apps-devhud-foundation.md), and affected `AGENTS.md` files for any service, RPC, message, enum, auth metadata, error, pagination, idempotency, generated package, or compatibility change.

## References

- [Project devhud](project-devhud.md)
- [RealQA server](servers-devhud-realqa-foundation.md)
- [DevHud app](apps-devhud-foundation.md)
- [Issue #757](https://github.com/delinoio/oss/issues/757)
- [Issue #756](https://github.com/delinoio/oss/issues/756)

## Out of Scope

- Public API/plugin SDK status, third-party clients/trackers, remote UI, GHES/custom hosts, mobile RealQA, production deployment, generated-client publication, GitHub App/extension registration, R2 provisioning, and catalog activation.
