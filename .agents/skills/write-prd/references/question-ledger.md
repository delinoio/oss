# PRD Decision Ledger

Use this ledger to make product and operational decisions explicit before drafting or filing an issue.

## Contents

- [Ledger Rules](#ledger-rules)
- [Product Intent](#product-intent)
- [Scope and Experience](#scope-and-experience)
- [Interfaces and Data](#interfaces-and-data)
- [Security and Integrations](#security-and-integrations)
- [Operations and Observability](#operations-and-observability)
- [Failure Modes](#failure-modes)
- [Documentation and Testing](#documentation-and-testing)
- [Issue Filing](#issue-filing)

## Ledger Rules

- Keep a row for every decision below, duplicating rows when multiple products, actors, services, or rollout paths need different answers.
- Use only `open`, `answered`, `not applicable`, or `contract-determined` as row states.
- Never close a row with a conventional default, best practice, or agent preference.
- For `answered`, record the user's explicit decision.
- For `not applicable`, record the user's explicit boundary and reason.
- For `contract-determined`, cite a repository path, issue, command result, schema, or API definition that directly decides the row.
- Reopen a row when later evidence or answers create a contradiction.

Use this compact shape while working:

```markdown
| Decision | Status | Result | Evidence or user answer |
| --- | --- | --- | --- |
| Goal | open |  |  |
```

## Product Intent

- Goal and user-visible outcome.
- Problem evidence or current-gap source.
- Primary, secondary, operator, system, and excluded actors.
- Observable or measurable success criteria.
- Priority, deadline, release driver, and urgency, including an explicit absence of timing constraints.

## Scope and Experience

- Exact capabilities, flows, commands, screens, APIs, jobs, or documents in scope.
- Adjacent work, future ideas, migrations, redesigns, and other exclusions.
- Non-goals and behavior the feature must not introduce.
- Upstream and downstream dependencies.
- Existing behavior, data, and contracts that must remain unchanged.
- Entry points and ordered happy path.
- Empty, loading, validation, error, permission, cancellation, and recovery states.
- User-facing terminology, messages, and naming.
- Accessibility, localization, and supported web, mobile, desktop, CLI, API, or operator surfaces.

## Interfaces and Data

- Public interfaces: Connect RPC, permitted REST exceptions, CLI flags, routes, events, streams, files, or webhooks.
- Request, response, event, stream, and error shapes.
- Identifier formats and externally visible references.
- Authorization, tenancy, and ownership at every interface.
- Backward compatibility, breaking-change intent, and client migration expectations.
- Contract documents that must change.
- Data model, source of truth, persistence, retention, deletion, and archival.
- Read models, caches, denormalized state, and invalidation.
- Idempotency, concurrency, ordering, replay, and retry semantics.
- Import, export, backfill, migration, and rollback data handling.

## Security and Integrations

- Authentication, authorization, and tenant, workspace, project, or user isolation.
- Secret, token, credential, and key handling.
- PII, sensitive data, audit, privacy, retention, and deletion requirements.
- Abuse prevention, rate limits, quotas, fraud controls, and required review gates.
- Internal services, external APIs, queues, databases, object stores, email, billing, or AI providers.
- Failure, timeout, retry, duplicate-delivery, and delivery-guarantee behavior for each integration.
- Environment variables, credentials, and configuration requirements.

## Operations and Observability

- Deployment units and infrastructure changes.
- Feature flags, staged rollout, cohorts, kill switch, rollback, and deployment compatibility.
- Latency, throughput, SLO, capacity, scaling, and cost expectations.
- Runbooks, operator actions, support workflows, and escalation paths.
- Structured logs needed for debugging and incident analysis.
- Metrics, traces, dashboards, alerts, audit events, and thresholds.
- Error classifications and correlation or debug identifiers exposed to users and operators.

## Failure Modes

- Malformed input and validation failure.
- Partial success, timeout, cancellation, duplicate submission, stale state, and races.
- Offline clients, degraded dependencies, rate limits, permission changes, and deleted resources.
- Cross-tenant, cross-workspace, cross-region, and high-volume behavior.

## Documentation and Testing

- Repository contracts, project docs, public docs, API docs, changelog, and release notes to update.
- Support tooling, administration, FAQ, troubleshooting, training, and migration communication.
- Observable acceptance criteria.
- Unit, integration, contract, end-to-end, migration, load, security, accessibility, and manual scenarios that apply.
- Fixtures, seeded data, mocks, and test environments.
- Explicit non-regression coverage.

## Issue Filing

- Current GitHub repository resolved from the active worktree.
- Contract-backed domain prefix and concise title description.
- Duplicate issue search terms and result.
- No labels, assignees, milestones, project fields, issue types, or other issue metadata.
