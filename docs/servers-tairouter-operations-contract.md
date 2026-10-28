# TaiRouter deployment and validation contract

## Scope

Owns server deployment integration and the supporting paths `packaging/tairouter` (Compose, npm distribution, observability examples) and `scripts/tairouter` (fixtures, security/integration validation, reproducible load tests). This server-domain contract owns these support paths because they deliver and verify the service and CLI together.

## Runtime and Language

Go server/CLI artifacts, Docker Compose, PostgreSQL, R2 integration, and Node.js for npm packaging. Official platform targets are defined by the deployment and CLI contracts.

For local source development, `servers/tairouter/package.json` and `turbo.json` register the API development task in the pnpm workspace. The [environment contract](repository-environment-contract.md#local-development) owns environment loading and the [workflow contract](repository-workflow-contract.md#development) owns root Turborepo orchestration. This does not change the deployment executable or root Go module.

## Users and Operators

Self-hosting operators, release maintainers, and validation authors.

## Interfaces and Contracts

### Deployment, recovery, and rollout

- Ship a single-host Docker Compose self-hosting workflow.
- Official server deployment targets: Linux x86_64 and ARM64.
- Document macOS/Windows Docker Desktop as development/evaluation environments.
- The operator's existing reverse proxy provides external HTTPS.
- TaiRouter serves internal HTTP; document TLS proxy examples and the public addresses needed for OIDC/provider OAuth callbacks.
- Do not deploy a hosted SaaS service.
- Use no feature flag: this is a new service, and deployment/rollback controls release. Authorization, tenant isolation, key validation, and limits remain enforced independently.
- Provide backup/restore commands, public operating guidance, and reproducible restore test scenarios.
- Backup execution, schedule, and retention are operator responsibilities; do not add a scheduled-backup product.
- Back up encryption keys separately. Exclude temporary continuation/device-handoff state from ordinary DB backups and accept its loss during disaster recovery.
- Maintenance downtime is permitted.
- Back up before upgrades; recover failures by restoring both the previous image and matching DB backup.
- **Before starting after a backup restore, invalidate all API keys, web sessions, invitations, and device authorizations.** Users must log in again, issue keys again, and reconnect CLIs.
- Do not promise fixed performance/SLOs. Publish repeatable load-test conditions, resource consumption, latency, and error rates.

### Local evaluation

The stable root `pnpm dev:tairouter` interface uses the separate `packaging/tairouter/compose.local.yml` stack and `scripts/tairouter/local.mjs` lifecycle wrapper. The [environment contract](repository-environment-contract.md#local-development) owns generated configuration, prerequisites, loopback bindings, state preservation, source-development selection and shutdown. Reuse the synthetic identity/SMTP fixture without changing production authentication or accepting fixture credentials in production. Wait for PostgreSQL and OIDC readiness and complete migration before API admission. No external account is required to open and sign in to the console. Generated media remains unavailable until R2 is configured; local evaluation does not establish provider or production acceptance.

### Observability and alert examples

Provide:

- Structured logs and request identifiers.
- Health-check endpoints.
- Prometheus metrics.
- OpenTelemetry traces.
- Grafana dashboard examples.
- DB-backed audit inspection under the roles above.

Do not record secret values or raw prompts/responses in logs/traces. Separate confirmed usage, unknown usage, write failures, cancellations, limits, and upstream/gateway errors.

Include only these default alert examples:

- Service health-check failure lasting one minute.
- DB connectivity-check failure lasting one minute.
- Cleanup failing three consecutive times, or no successful cleanup for 15 minutes.

Cleanup runs every minute. Operators configure error-rate and capacity alerts themselves; no fixed 5% error-rate or 85% disk alert is required.

Observability must not expand instance operators' whole-instance health/aggregate access into organization-internal inspection.

## Storage

Ordinary backups exclude encrypted temporary continuation and device-handoff storage. Back up encryption keys separately; retain old keys needed for old backup restoration. R2 objects and metadata obey the [service artifact lifecycle](servers-tairouter-service-contract.md).

## Security

Require deployment configuration for generic OIDC, the operator `issuer+sub` allowlist, SMTP, PostgreSQL, versioned encryption keys, R2, public callback addresses, and approved private/local destinations. Validate these boundaries before service admission. No root environment example or committed credentials. Changes to the operator list require deployment configuration, not organization privilege assignment. Restore invalidation must complete before admitting traffic.

## Logging

The observability rules above apply to every deployable, packaging example, fixture, and dashboard. Audit visibility is role-scoped by the service contract.

## Build and Test

### Release gates

- [ ] The owning project/domain contracts exist before runtime implementation and document the pinned reference and explicit deviations.
- [ ] A complete interface/provider/authentication/client integration matrix maps the fixed reference to implementation and contract tests, including exclusions and platform-specific constraints.
- [ ] Included inference APIs preserve their supported wire behavior, streams, errors, routing, continuation, and retry/timeout semantics.
- [ ] Organization/user isolation holds across requests, state, credentials, caches, retries, jobs, and metadata inspection.
- [ ] OIDC, role changes, key lifecycle, invitations, ownership transfer, and deletion obey the exact authorization and race boundaries above.
- [ ] Limits, cancellation, DB outage, unknown usage, and revoked/removed principals produce the specified observable results.
- [ ] PostgreSQL temporary state is encrypted, bounded, backup-excluded, and inaccessible immediately at expiry; cleanup runs every minute.
- [ ] Generated media artifacts use R2, the shared per-user 200-file quota, 24-hour TTL, separate image/video size limits, and correctly bounded signed URLs.
- [ ] Price changes do not rewrite historical cost estimates; unknown values and missing counts remain visible.
- [ ] The CLI supports approved platforms, device approval, OS credential storage, named profiles, explicit settings confirmation, safe restoration, and required loopback relays.
- [ ] Local program execution as a provider and the associated execution-agent features are absent.
- [ ] Compose installation/upgrade/restore fixtures cover the documented operational boundaries, including post-restore invalidation and master-key rotation.
- [ ] Logs, metrics, traces, audits, dashboards, and three default alert examples satisfy access and redaction rules.
- [ ] Public guidance and internal design/contracts cover the agreed scope, including the separately approved console design; internal runbooks remain excluded.
- [ ] Automated security/integration tests and code review are complete.
- [ ] Reproducible load tests publish workload conditions, resource use, latency, and error rates without asserting a fixed guarantee.
- [ ] Mock/build/contract/integration results are clearly separated from live provider/account, production, and native OS verification. Live verification is not a first-release gate.
- [ ] Distribution/license checks and npm publication authority are verified before publishing artifacts.

### Reproducible scenarios

#### Reproducible test environment

Include all of the following without real provider credentials:

- Test OIDC provider.
- SMTP receiver.
- Mock AI providers.
- PostgreSQL.
- Multiple organizations/users and seeded test data.
- Reproducible error, streaming, and retry behavior.

Use this environment for the agreed development, CI, integration, and reproducible load-test work. Real-environment, real-account, and actual OS acceptance are excluded from required first-release validation; report actual verification separately.

#### Required scenarios

1. **Protocol parity:** streaming/non-streaming, tools, model discovery/selection, continuation, account pools, OAuth, combinations, image routes, and all included provider-specific retry/timeout cases. Do not count excluded local execution paths as supported.
2. **Tenant isolation:** same user across organizations, different users in one organization, colliding object/conversation IDs, cache and retry paths, and metadata/audit access.
3. **Roles:** owner/admin/member/operator boundaries, live demotion, pending ownership transfer cancellation/replacement/expiry, and authority changes before acceptance.
4. **OIDC/session:** issuer/sub identity, no email merge, missing/unverified email rejection, inactivity/absolute expiry, and logout.
5. **Invitations:** verified-email match, single-use/expiry/cancel/resend, transient/permanent/ambiguous SMTP failures, retry intervals, hourly limits, job races, and final link invalidation.
6. **Keys:** hash verification, no-expiry and selected expiry, revocation during a request, new-request denial, and prevention of additional retries/account switches/token refresh.
7. **Device approval:** ten-minute expiry, five-second polling, denial, exactly one key per approval, device-secret isolation, interrupted handoff, storage confirmation, temporary raw-key deletion, and revocation of unclaimed keys.
8. **Local credentials:** unavailable OS store, unchanged settings on failure, compensating server-key revocation, outstanding-key guidance, and explicit confirmation for client settings containing a key.
9. **CLI settings/services:** target-platform builds and mocked credential-store/service/permission behavior, backup and restore, preservation of edits, conflict recovery, multi-profile ports, retained keys during switches, port-conflict failure, and offline disconnect.
10. **Temporary state:** encryption, TTL/count/byte boundaries, oldest-first eviction, explicit continuation failure, immediate expiry denial, one-minute cleanup, and no cross-tenant restoration.
11. **Image artifacts:** per-organization/user limits, 50 MiB image and 100 MiB response boundaries, 200-file eviction, 24-hour expiry, authorization at URL issuance, five-minute maximum URL lifetime bounded by artifact expiry, and unavailable deleted objects.
    **Hosted video artifacts:** separate 200 MiB per-video and 600 MiB per-turn boundaries, the shared image/video file quota, and the same expiry, publication, deletion, and download authorization rules. Test indirect downloads against destination policy, cancellation, and the shared physical-send budget.
12. **Deletion races:** membership removal, organization/account deletion, running responses, token refresh, pending jobs, and logging do not recreate deleted private state; retained records obey anonymization and retention policies.
13. **Failures:** DB outage produces 503 for new requests, permits only dispatched response completion, stops further sends, and exposes usage-write failure. Test client cancellation, partial streams, and unknown usage.
14. **Pricing:** absent usage/price is not zero, missing counts are exposed, only operators edit common prices, historical estimates remain fixed, and reference cache/tier rules are preserved.
15. **Security:** destination-policy bypass, secret/content exposure, device-code replay, session/key invalidation, and tenant boundaries.
16. **Recovery:** temporary-state exclusion from backup, key separation, maintenance re-encryption, previous-image/DB restoration, and invalidation of keys/sessions/invitations/device approvals before service admission.
17. **Observability:** request IDs, metrics/traces, role-scoped audits, all three alert conditions, and unknown/failure outcomes never represented as successful recording.
18. **Web/accessibility:** English, responsive behavior, keyboard/focus/dialog requirements, and WCAG 2.2 AA checks within the stated automated validation boundary. Use the separately approved [console design](apps-tairouter-console-design.md).
19. **Non-regression:** unrelated client settings and user edits survive configuration changes; other tenants/users remain unchanged; repository workflow/security/license boundaries are preserved.
20. **Load:** deterministic workload reproduction with recorded hardware/resources, concurrency, latency, and error results; do not conflate this with live-provider capacity or a guaranteed SLO.

Record source revision, commands, results, and unperformed checks. Exclude secrets and real user content. Follow the repository's applicable tests, including frontend `pnpm test` after frontend changes and root `cargo test` if Rust is changed. Preserve the prohibition on adding macOS runners to PR, main-push, and affected manual CI; native verification is not a release gate here.

Schema-only Compose validation uses the committed `.env.example` for interpolation and service environment-file references. It must run without a deployment `.env`, must not create or read one, and must not print the resolved environment. This validation-only override does not change deployment or recovery fixture configuration.

### Publication

Use standalone Go binaries with checksums and the `@delino/tairouter` npm installer for matching platform binaries. An earlier public npm lookup returned HTTP 404; this proves neither scope ownership nor name reservation nor publishing permission. Verify authority before publication. Include Apache-2.0 terms and all imported licenses/notices in distributions. The common `Release Project` coordinator prepares the version in `packaging/tairouter/npm/package.json` and the `tairouter@v<version>` tag. `.github/workflows/release-tairouter.yml` handles tag-triggered publication and manual recovery with an exact source version and `dry_run=true` by default. Publication requires the existing remote tag to resolve to the workflow commit; never move a release tag.

The release workflow runs generated-client, console, race, security/integration, Compose recovery, alert and synthetic-load gates before publication. It stages the existing five CLI targets, two Linux server binaries, npm tarball, licenses/notices and a Compose archive with checksums and Sigstore bundles. It builds the bundled-console image for Linux AMD64/ARM64 and publishes `ghcr.io/delinoio/tairouter:<version>` with source revision labels. Dry runs build both architectures without registry login, signing or publication. GitHub contents and GHCR package writes use job-scoped workflow-token permissions. Before public distribution, maintainers must confirm that the GHCR package is publicly readable; initial package visibility is an account configuration step.

npm publication follows GitHub Release publication and requires `TAIROUTER_NPM_PUBLISH_ENABLED=true`. Keep it disabled until maintainers verify scope ownership and publishing authority and configure npm trusted publishing for `delinoio/oss`, workflow `release-tairouter.yml`, environment `tairouter-npm`. The job uses OIDC and provenance without a long-lived npm token. The runner requires npm 11.5.1 or later under the Node.js baseline. A skipped npm job does not establish npm availability. Bootstrap publication, if needed to configure a trusted publisher, remains an account-owner action.

The workflow configures artifact publication only; operators still configure and run their own Compose deployment. It does not deploy a hosted service or establish production/live/native acceptance. Rerun failed downstream jobs for the same tag and revision. Existing GitHub releases are never overwritten: inspect an existing or partial release before manual recovery. Registry image tags should be pinned by digest in production; retain the previous digest for rollback.

There is no fixed deadline. Release only after mock, contract, integration, security, build, deployment-fixture, and reproducible load-test gates pass. Live provider/account, real deployment, and native OS acceptance are not first-release gates; report their actual status separately. Do not claim performance/SLO guarantees.

## Dependencies and Integrations

- [servers-tairouter-service-contract](servers-tairouter-service-contract.md)
- [cmds-tairouter-client-contract](cmds-tairouter-client-contract.md)
- [apps-tairouter-console-contract](apps-tairouter-console-contract.md)
- [apps-tairouter-docs-contract](apps-tairouter-docs-contract.md)
- [protos-tairouter-management-contract](protos-tairouter-management-contract.md)
- [packages-tairouter-libraries-contract](packages-tairouter-libraries-contract.md)
- [packages-tairouter-client-contract](packages-tairouter-client-contract.md)

## Change Triggers

Update this contract, the project index, and affected producer/consumer contracts together when interfaces, ownership, or lifecycle rules change. Follow the [instruction-update policy](README.md#instruction-update-policy); behavior belongs in contracts, not instruction files. Record implementation status, validation results, and unresolved limits in GitHub records or CI artifacts.

## References

- [Project index](project-tairouter.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)

## Monorepo integration

Use `pnpm dev:tairouter` for local Compose evaluation and `pnpm dev:tairouter:source` for configured source development and the centrally planned `tairouter` job in `.github/workflows/CI.yml` for affected fixture validation. These replace the standalone repository root commands and CI workflow. Public docs are built through `apps/labs-docs`, including all existing English TaiRouter guides at `https://docs.labs.delino.io/tairouter/`. Stage future release assets under the `tairouter@v<version>` tag namespace; staging does not publish artifacts; the separate release workflow owns publication.
