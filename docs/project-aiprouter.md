# AIPRouter project index

## Goal

Build a self-hosted, organization-based, multi-tenant and multi-user AI provider router. Each user exclusively uses personal provider credentials, account pools, and routing within each organization. Deliver a Go service and client configuration CLI, an English responsive React console, and a Docker Compose deployment. PostgreSQL owns durable and encrypted temporary state; R2 holds temporary generated images and hosted video output under the service media limits.

## Project ID

`aiprouter`

## Domain Ownership Map

The following canonical paths define component ownership. Contracts describe required behavior; they do not establish implementation completeness or validation results.

| Domain/component  | Canonical path                                  | Ownership and integration boundary                                            |
| ----------------- | ----------------------------------------------- | ----------------------------------------------------------------------------- |
| Go module         | Root `go.mod`, module `github.com/delinoio/oss` | Single module for service, CLI, shared Go, generated Go                       |
| Service           | `servers/aiprouter`                             | Go executable; management and inference admission, adapters, storage and jobs |
| CLI               | `cmds/aiprouter`                                | Go executable; OS credentials, settings, profiles, loopback relay             |
| Shared Go         | `packages/aiprouter`                            | Shared mechanisms; consuming service/CLI retains policy ownership             |
| Protocol          | `protos/aiprouter/v1`                           | Protobuf source and generated Go bindings                                     |
| TypeScript client | `packages/aiprouter-client`                     | Generated management bindings for console                                     |
| Console           | `apps/aiprouter`                                | React/Rsbuild console; generated Connect RPC consumer                         |
| Public docs       | `apps/public-docs/docs/aiprouter`               | Consolidated Rspress publication; curated user/operator guidance              |
| Distribution      | `packaging/aiprouter`                           | Compose/npm/observability assets; operations contract owns integration        |
| Validation        | `scripts/aiprouter`                             | Reproducible fixtures and validation; operations contract owns release gates  |

The service workspace manifest and package-level Turbo configuration expose its Go development command to root `pnpm dev:aiprouter`. The console and consolidated public documentation workspace commands join the same persistent, uncached Turbo task under the [workflow contract](repository-workflow-contract.md#development).

## Domain Contract Documents

- [servers-aiprouter-service-contract](servers-aiprouter-service-contract.md)
- [servers-aiprouter-compatibility-contract](servers-aiprouter-compatibility-contract.md)
- [servers-aiprouter-operations-contract](servers-aiprouter-operations-contract.md)
- [cmds-aiprouter-client-contract](cmds-aiprouter-client-contract.md)
- [protos-aiprouter-management-contract](protos-aiprouter-management-contract.md)
- [packages-aiprouter-libraries-contract](packages-aiprouter-libraries-contract.md)
- [packages-aiprouter-client-contract](packages-aiprouter-client-contract.md)
- [apps-aiprouter-console-contract](apps-aiprouter-console-contract.md)
- [Console design](apps-aiprouter-console-design.md)
- [apps-aiprouter-docs-contract](apps-aiprouter-docs-contract.md)

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

Later upstream revisions require a separate scope decision. The explicit AIPRouter decisions and exclusions below take precedence over reference behavior.

### Product and compatibility boundary

Include the pinned reference's public inference APIs, provider/authentication integrations, protocol translation, streaming, tool calling, continuation, model routing, account pools, OAuth, combination routing, and usage dashboard functionality, subject to the explicit exceptions below.

Preserve request, response, stream, error, model selection, retry, and continuation semantics per supported path. Matching feature names alone is insufficient. Inventory all included interfaces and provider exceptions against the fixed revision.

The inspected inbound routes include Responses and its WebSocket transport, Responses compaction, Chat Completions, Anthropic Messages and token counting, model discovery, hosted search, image generation/editing, and artifact retrieval. This is an evidence-backed starting list, not a substitute for the complete inventory.

**Exclude provider paths that execute local programs such as Claude CLI, CodeBuddy, or Qoder on the user's computer to call a provider.** Do not move those execution paths to the server as a substitute. Also exclude the associated WSS execution agent, execution-target selection, remote command dispatch, workspace selection, and tool-execution permissions.

Keep these distinct:

- Configuring a client to connect to AIPRouter remains in scope.
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
- Before runtime implementation, add `docs/project-aiprouter.md`, the owning domain contracts, and catalog entries in `docs/README.md`. Document component ownership and integration boundaries.
- Follow the target repository's configuration, secret, dependency security, formatting, and CI contracts. Do not import unrelated project-specific assumptions from another repository.
- Update AGENTS.md only for changes to development procedures, ownership, or development rules; keep feature behavior in its owning contracts.

### Integration and default boundaries

- Organization plus user scopes all personal credentials, pools, routing, temporary state, jobs, caches, retries, and metadata access. No shared organization credential pools.
- `owner`, `administrator`, and `member` identify organization roles; `instance-operator` is separate deployment authority. The service contract owns exact permissions and lifecycle races.
- PostgreSQL remains authoritative across instances and restarts; fail closed on authorization storage failure. OS credential stores own CLI secrets. R2 signed URLs are bounded bearer capabilities, not fresh authorization on each download.
- CLI recovery journals retain owner-protected, content-addressed snapshots for large native catalogs under the [client storage contract](cmds-aiprouter-client-contract.md#storage); this does not change OS credential-store ownership.
- Console and CLI management use versioned Connect RPC schemas; inference protocols remain wire-compatible with the fixed reference and explicit overrides.
- Console navigation and OIDC return targets accept only canonical paths and workspace selectors from the [console route contract](apps-aiprouter-console-contract.md#page-routes). Retired console aliases and query-based navigation are unsupported; invitation emails use `/invitation` and the default login destination is `/overview`.
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
- Concrete visual decisions in the initial onboarding brief; the separately approved [console design](apps-aiprouter-console-design.md) now owns implementation layouts.
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
