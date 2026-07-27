# protos-devhud-realqa-api-contract

## Scope

- Project/component: `devhud` / `realqa-api`
- Canonical source path: `protos/devhud-realqa/v1`
- Contract identity: `devhud.realqa.v1`
- Status: planned for issue #757; no source, generated package, deployed API, or published client is claimed.

## Runtime and Language

- Versioned Protobuf is authoritative.
- Generate reproducible Connect-compatible Go and protobuf-es v2 TypeScript artifacts under `protos/devhud-realqa/gen/go` and `protos/devhud-realqa/gen/ts`; generated files are derived and never a second contract.
- The future workspace TypeScript package is `@delinoio/devhud-realqa-connect`.
- Preserve released v1 fields additively. Breaking changes require `devhud.realqa.v2` and synchronized consumer migration docs.

## Services and RPCs

- `RealQAPresetService`: `ListPresets`, `GetPreset`, `CreatePreset`, `UpdatePreset`, `DeletePreset`, `DeleteFeatureData`.
- `RealQATrackerService`: `GetGitHubConnection`, `StartGitHubConnection`, `ListGitHubInstallations`, `DisconnectGitHubConnection`, `ListRepositories`, `GetRepositoryIssueSchema`.
- `RealQASubmissionService`: `CreateSubmission`, `CreateImageUpload`, `FinalizeImageUpload`, `SubmitIssue`, `GetSubmission`, `DeleteImage`, `DeleteSubmissionAssets`.

No additional v1 service or RPC is implied. GitHub callback/webhook, same-origin signed image PUT, and public image GET handlers are narrow HTTP/server boundaries.

## Wire Contract

- IDs and idempotency keys are UUID v7 wrappers. Owner scope, capture mode, selector mode, tracker kind, submission state, upload state, failure class, asset state, and stable error reason are closed enums.
- The only v1 tracker is GitHub.com. The internal tracker abstraction is not exposed as a plugin protocol.
- Presets carry owner/billing scope, capture/pointer/selector defaults, destination/repository, template/form choice, supported provider extensions, ordered safe process/title URL mappings, shortcut, and revision.
- Lists use opaque cursors. Enforce 50 personal presets, 250 organization presets, and 20 active device shortcuts.
- Synchronized writes carry expected revision and return a new revision; typed conflicts support reload/compare/reapply.
- Submission creation carries the stable local idempotency key. Upload messages carry bounded metadata needed for a short-lived signed PUT at exactly `https://assets.realqa.deli.dev` and verification, never an R2 S3 endpoint or raw image bytes through Connect.
- The API represents no explicit image-count limit, while errors enforce 25 MiB/encoded image, 250 MiB/session, 100 megapixels/decoded image, and 60,000 UTF-8 bytes/final issue body.
- Final submission carries explicit public-image confirmation. Reconciliation state represents ambiguous GitHub responses and the hidden marker without exposing the marker as a user-editable identity.
- Deletion messages distinguish image/range deletion, submission-asset deletion, and owner-scoped feature deletion. `DeleteFeatureData` carries personal or organization owner scope plus an idempotency key; a personal caller may delete only their own data, while organization deletion requires an Owner. The first accepted request blocks new access and asynchronously removes the scope's presets, submissions, and assets; exact replays return the same deletion-job result. Responses preserve the stable removed-placeholder URL contract and required pseudonymized financial/security records.
- Client-provided role, GitHub permission, billing authorization/price, upload verification, provider success, and asset state are never authoritative.

## Authentication, Privacy, and Errors

- Protected RPCs require the RealQA-audience user token and dedicated memory-only delibase-audience forwarded bearer metadata. Generated clients keep both out of messages, logs, errors, caches, persistence, and diagnostics.
- Errors distinguish authentication/reauthentication, authorization, stale revision, disconnected/unsupported host, provider permission/schema/validation, body/image/session limits, malformed/unsupported/decompression-bomb input, upload verification, ambiguous reconciliation, rate/concurrency limit, billing reservation/background-authorization/grace, asset removed, and retention state.
- Messages must not carry GitHub tokens, webhook/R2 secrets, raw authorization headers, production secrets, raw originals, arbitrary HTML/page text, or remotely supplied UI/code.
- Submitted title/body/URL/DOM content must not remain in ordinary server messages/storage after reconciliation beyond the response needed by the active operation.

## Build and Test

Once implementation exists, canonical checks are:

- `pnpm generate:proto`;
- `pnpm check:proto`;
- `go test ./protos/devhud-realqa/...`;
- `go vet ./protos/devhud-realqa/...`;
- `pnpm --filter @delinoio/devhud-realqa-connect typecheck`.

Generation must lint, check the immutable descriptor baseline, generate Go/TypeScript artifacts, build the TypeScript package, and reject a generated diff. CI fails on compatibility, service/RPC/enum drift, stale artifacts, sensitive metadata leakage, or missing cross-consumer generation.

Checks do not publish the TypeScript package, deploy either RealQA origin, create R2 infrastructure, register the GitHub App, publish the extension, activate a catalog entry, or publish a server image.

## Dependencies and Change Triggers

- Owned by `devhud`; consumed only by `servers/devhud-realqa` and authenticated RealQA code under `apps/devhud`.
- Recurring storage settlement additionally depends on the separately synchronized delibase background-usage contract in issue #756. RealQA implementation and activation are blocked until that contract exposes its bounded human authorization and M2M authorized-usage RPCs; the existing live forwarded-token `ReserveUsage`/`CommitUsage`/`ReleaseUsage` RPCs are not a substitute.
- Update this document, [project-devhud](project-devhud.md), [servers-devhud-realqa-foundation](servers-devhud-realqa-foundation.md), [apps-devhud-foundation](apps-devhud-foundation.md), and affected `AGENTS.md` files for any service, RPC, message, enum, auth metadata, error, pagination, idempotency, generated package, or compatibility change.

## References

- [Project devhud](project-devhud.md)
- [RealQA server](servers-devhud-realqa-foundation.md)
- [DevHud app](apps-devhud-foundation.md)
- [Issue #757](https://github.com/delinoio/oss/issues/757)
- [Issue #756](https://github.com/delinoio/oss/issues/756)

## Out of Scope

- Public API/plugin SDK status, third-party clients/trackers, remote UI, GHES/custom hosts, mobile RealQA, production deployment, generated-client publication, GitHub App/extension registration, R2 provisioning, and catalog activation.
