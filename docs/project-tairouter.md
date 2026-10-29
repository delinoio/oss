# TaiRouter project index

## Goal

TaiRouter means **Team AI Router** and is an open-source Delino Labs product. Build a self-hosted, organization-based, multi-tenant and multi-user AI provider router. Each user exclusively uses personal provider credentials, account pools, and routing within each organization. Deliver a Go service and client configuration CLI, a multilingual responsive React console, and a Docker Compose deployment. PostgreSQL owns durable and encrypted temporary state; R2 holds temporary generated images and hosted video output under the service media limits.

## Project ID

`tairouter`

### Naming boundary

TaiRouter replaces the former AIPRouter name across repository paths, executables, workspace packages, configuration, management RPCs, storage namespaces, and distribution assets. The canonical identifiers are `tairouter`, `TAIROUTER_*`, `@tairouter/*`, `@delino/tairouter`, and `tairouter.v1`. Public documentation uses `https://docs.labs.delino.io/tairouter/`; product marketing uses `https://labs.delino.io/tairouter/`.

This rename does not provide legacy aliases or automatic migration of existing deployments, local evaluation volumes, CLI state, OS credential-store entries, or database schemas. Preserve the previous installation and its protected backups before evaluating the renamed installation. Do not treat an existing database as a fresh TaiRouter database or mix old and new service/CLI versions. The [public migration guide](../apps/labs-docs/docs/en/tairouter/guide/migration.md) describes the user-facing boundary.

## Domain Ownership Map

The following canonical paths define component ownership. Contracts describe required behavior; they do not establish implementation completeness or validation results.

| Domain/component  | Canonical path                                  | Ownership and integration boundary                                            |
| ----------------- | ----------------------------------------------- | ----------------------------------------------------------------------------- |
| Go module         | Root `go.mod`, module `github.com/delinoio/oss` | Single module for service, CLI, shared Go, generated Go                       |
| Service           | `servers/tairouter`                             | Go executable; management and inference admission, adapters, storage and jobs |
| CLI               | `cmds/tairouter`                                | Go executable; OS credentials, settings, profiles, loopback relay             |
| Shared Go         | `packages/tairouter`                            | Shared mechanisms; consuming service/CLI retains policy ownership             |
| Protocol          | `protos/tairouter/v1`                           | Protobuf source and generated Go bindings                                     |
| TypeScript client | `packages/tairouter-client`                     | Generated management bindings for console                                     |
| Console           | `apps/tairouter`                                | React/Rsbuild console; generated Connect RPC consumer                         |
| Public docs       | `apps/labs-docs/docs/en/tairouter`              | Labs Rspress publication; English user/operator guidance                      |
| Marketing         | `apps/labs-site/tairouter.html`                 | Localized static product page at `https://labs.delino.io/tairouter/`          |
| Distribution      | `packaging/tairouter`                           | Compose/npm/observability assets; operations contract owns integration        |
| Validation        | `scripts/tairouter`                             | Reproducible fixtures and validation; operations contract owns release gates  |

The service executable composes `internal/service`, `internal/config`, `internal/console`, `internal/auth`, and `internal/observability` under `servers/tairouter`. The [service package boundaries](servers-tairouter-service-contract.md#internal-package-boundaries) define ownership and dependency direction. These packages share one deployment and the existing root Go module.

Root `pnpm dev:tairouter` provides the local Compose evaluation described in the [environment contract](repository-environment-contract.md#local-development), including generated private configuration and automatic migrations. `scripts/tairouter/local.mjs` owns startup, logs and stop; `packaging/tairouter/compose.local.yml` isolates evaluation from production assets.

The service workspace manifest and package-level Turbo configuration expose its Go development command to root `pnpm dev:tairouter:source`. The console and Labs documentation workspace commands join the same persistent, uncached Turbo task under the [workflow contract](repository-workflow-contract.md#development).

Affected validation runs in the centrally planned `tairouter` job in `.github/workflows/CI.yml`. The [workflow contract](repository-workflow-contract.md) owns selection and final result aggregation; the [operations contract](servers-tairouter-operations-contract.md) owns validation coverage and acceptance boundaries.

The common `Release Project` coordinator prepares TaiRouter versions and tags. `.github/workflows/release-tairouter.yml` owns fixture-gated binary, Compose, npm and Linux container publication under the [operations contract](servers-tairouter-operations-contract.md#publication). Public documentation keeps its existing Labs Docs publisher.

## Domain Contract Documents

- [servers-tairouter-service-contract](servers-tairouter-service-contract.md)
- [servers-tairouter-compatibility-contract](servers-tairouter-compatibility-contract.md)
- [servers-tairouter-operations-contract](servers-tairouter-operations-contract.md)
- [cmds-tairouter-client-contract](cmds-tairouter-client-contract.md)
- [protos-tairouter-management-contract](protos-tairouter-management-contract.md)
- [packages-tairouter-libraries-contract](packages-tairouter-libraries-contract.md)
- [packages-tairouter-client-contract](packages-tairouter-client-contract.md)
- [apps-tairouter-console-contract](apps-tairouter-console-contract.md)
- [Console design](apps-tairouter-console-design.md)
- [apps-tairouter-docs-contract](apps-tairouter-docs-contract.md)

## Cross-Domain Invariants

### Fixed compatibility reference

The requested product is a multi-tenant/multi-user version of [opencodex](https://github.com/lidge-jun/opencodex). Pin compatibility to:

- Repository: `lidge-jun/opencodex`.
- Revision: **`88aaa754566d119b6b8fc898f8fb47602387d82e`**, the main HEAD observed during investigation.
- Commit timestamp: `2026-10-27T05:46:19Z`.
- [Pinned source tree](https://github.com/lidge-jun/opencodex/tree/88aaa754566d119b6b8fc898f8fb47602387d82e).
- [Providers and adapters](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/structure/providers-and-adapters.md).
- [Transport inventory](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/structure/transports/inventory.md).
- [Client configuration registry](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/integrations/registry.ts).
- [Inbound routes, including image generation and editing](https://github.com/lidge-jun/opencodex/blob/88aaa754566d119b6b8fc898f8fb47602387d82e/src/server/index/serve-options.ts).

Later upstream revisions require a separate scope decision. The explicit TaiRouter decisions and exclusions below take precedence over reference behavior.

### Console installation

The console supports browser-managed PWA installation with a stable `/overview` launch route. Installation metadata is owned by the [console contract](apps-tairouter-console-contract.md#installable-web-app), and static serving is owned by the [service contract](servers-tairouter-service-contract.md#console-route-integration). Installation requires a secure context and does not provide offline management.

### Product and compatibility boundary

Include the pinned reference's public inference APIs, provider/authentication integrations, protocol translation, streaming, tool calling, continuation, model routing, account pools, OAuth, combination routing, and usage dashboard functionality, subject to the explicit exceptions below.

Preserve request, response, stream, error, model selection, retry, and continuation semantics per supported path. Matching feature names alone is insufficient. Inventory all included interfaces and provider exceptions against the fixed revision.

The inspected inbound routes include Responses and its WebSocket transport, Responses compaction, Chat Completions, Anthropic Messages and token counting, model discovery, hosted search, image generation/editing, and artifact retrieval. This is an evidence-backed starting list, not a substitute for the complete inventory.

**Exclude provider paths that execute local programs such as Claude CLI, CodeBuddy, or Qoder on the user's computer to call a provider.** Do not move those execution paths to the server as a substitute. Also exclude the associated WSS execution agent, execution-target selection, remote command dispatch, workspace selection, and tool-execution permissions.

Keep these distinct:

- Configuring a client to connect to TaiRouter remains in scope.
- An HTTP loopback relay remains in scope.
- HTTP-based local model servers remain in scope, subject to operator-approved private/local destinations.
- Executing a client program as a provider backend is out of scope.

The earlier execution-agent proposal is superseded by this exclusion.

### Implementation architecture and repository contracts

- Implement the server and CLI anew in **Go**. Do not adopt opencodex as a bundled server engine.
- Use **PostgreSQL** as the source of truth for application state, membership, key revocation, limits, and temporary continuation state.
- Use **React, TypeScript, and Rsbuild** for the web console.
- Use **React Query**, with **`@connectrpc/connect-query`** for Connect RPC.
- Use **Connect RPC, Protobuf contracts, and generated clients** for organization, member, key, provider, usage, and operator management flows.
- Provide the interfaces required by the web console and CLI connection flow. A stable public management API for external automation is not part of the first release. Inference keys do not implicitly grant management permissions.
- Use UUID v7 for new persisted entities under the repository default, while preserving externally required protocol identifier shapes.
- License repository-owned source and distributions under Apache-2.0. Preserve original licenses/notices for any imported source or data.
- Before runtime implementation, add `docs/project-tairouter.md`, the owning domain contracts, and catalog entries in `docs/README.md`. Document component ownership and integration boundaries.
- Follow the target repository's configuration, secret, dependency security, formatting, and CI contracts. Do not import unrelated project-specific assumptions from another repository.
- Update AGENTS.md only for changes to development procedures, ownership, or development rules; keep feature behavior in its owning contracts.

### Integration and default boundaries

- Organization plus user scopes all personal credentials, pools, routing, temporary state, jobs, caches, retries, and metadata access. No shared organization credential pools.
- `owner`, `administrator`, and `member` identify organization roles; `instance-operator` is separate deployment authority. The service contract owns exact permissions and lifecycle races.
- PostgreSQL remains authoritative across instances and restarts; fail closed on authorization storage failure. OS credential stores own CLI secrets. R2 signed URLs are bounded bearer capabilities, not fresh authorization on each download.
- CLI recovery journals retain owner-protected, content-addressed snapshots for large native catalogs under the [client storage contract](cmds-tairouter-client-contract.md#storage); this does not change OS credential-store ownership.
- CLI relay teardown is limited to the selected state directory's verified user service. A matching profile name in another state directory does not grant authority over that service; the [client contract](cmds-tairouter-client-contract.md#http-relay-lifecycle-and-trust-boundary) owns conflict handling.
- Console and CLI management use versioned Connect RPC schemas; inference protocols remain wire-compatible with the fixed reference and explicit overrides.
- Console navigation and OIDC return targets accept only canonical paths and workspace selectors from the [console route contract](apps-tairouter-console-contract.md#page-routes). Organization scope uses `/organizations/<organization-id>/<page>`; existing `organization` query bookmarks normalize to those paths. Retired console aliases and `page` query navigation are unsupported; invitation emails use `/invitation` and the default login destination is `/overview`.
- The self-hosted authenticated console uses Compose rather than the default static Cloudflare Pages hosting. The public docs application uses the default Cloudflare Pages publication target.
- Hosted inference search preserves the reference provider interface; it is not a new generic AI-search backend. Do not replace that compatibility path with Cloudflare AI Search. A future independent search feature must follow repository defaults or record a separate decision.
- Runtime implementation must produce the complete compatibility matrix, concrete schemas/generated interfaces, and publication checks. The listed reference examples are not an exhaustive inventory. Report contradictions; do not silently omit providers or weaken isolation.

### Exclusions

- Hosted SaaS deployment and platform/region selection.
- Service charging, billing, subscriptions, and payment processing.
- Sharing personal provider credentials or pools among organization members.
- Executing local CLI programs as provider backends, WSS execution agents, and remote shell/workspace/tool-execution features.
- A standalone desktop application.
- A stable public management API for external automation.
- Automatic import of existing opencodex settings, secrets, or usage.
- User-facing data export.
- Permanent prompt/response or generated-image retention.
- Feature flags and staged flag rollout.
- An instance-wide inference concurrency cap.
- Fixed performance or SLO guarantees.
- External penetration testing or compliance certification as first-release requirements.
- Mandatory live provider/account, production-environment, or actual OS acceptance.
- Internal runbooks.
- Concrete visual decisions in the initial onboarding brief; the separately approved [console design](apps-tairouter-console-design.md) now owns implementation layouts.
- Runtime implementation or deployment as part of documentation-only onboarding.

## Change Policy

Update this index and affected domain contracts in the same change as ownership, interface, or structural changes. Security and lifecycle changes must update all affected service, CLI, protocol, console, and operations consumers. Preserve every explicit compatibility override and exclusion; later upstream revisions require a separate scope decision.

Update scoped instructions only for development procedures, ownership, or rules, following the [instruction-update policy](README.md#instruction-update-policy). Keep new implementation status, validation results, source revisions, commands, unresolved limits, and traceability in GitHub records or CI artifacts. Do not include secrets or real user content. There is no fixed deadline or guaranteed SLO; the operations contract defines first-release gates and distinguishes mock/build evidence from live/native acceptance.

The initial onboarding scope excluded concrete visual decisions; the separately approved console design now defines them. No unrelated labels, assignees, milestones, project fields, or metadata are part of onboarding.

## References

- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
- [Environment](repository-environment-contract.md)
- [Licensing](repository-license-contract.md)
- [Dependency security](repository-dependency-security-contract.md)
- [Project template](project-template.md)
- [Domain template](domain-template.md)
