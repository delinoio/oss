# PRD GitHub Issue Template

Create one issue for one coherent feature or implementation slice. Apply the current repository's issue contract if it is stricter than this reference.

## Title

Use this shape:

```text
<domain>: <description>
```

- Select `<domain>` from a stable lowercase project or domain identifier in repository contracts.
- Keep `<description>` concise and specific, starting with a lowercase verb phrase when natural.
- Do not use bracketed project prefixes.

## Body

Use these sections in this exact order:

```markdown
## Summary
Describe the feature, intended users, core value, and observable success in one or two concise paragraphs.

## Evidence
- Request or evidence source:
- Current gap:
- Users or actors affected:
- Repository or contract evidence:
- Duplicate issue search:

## Current Gap
Explain the current behavior or missing capability that motivates the change.

## Proposed Scope
Define the agreed functional and operational behavior. Cover applicable interfaces, data and state, UX, authorization, security and privacy, integrations, rollout and rollback, observability, documentation, and support decisions.

## Acceptance Criteria
- A user, operator, or system can ...
- Interface, authorization, ownership, validation, and error behavior are ...
- Operational controls and observability are ...
- Required documentation and support artifacts are ...

## Test Scenarios
- Verify the primary happy path.
- Verify validation, authorization, error, and recovery behavior.
- Verify applicable interface, data, migration, integration, and compatibility behavior.
- Verify applicable rollout, rollback, observability, and support behavior.
- Verify preserved behavior does not regress.

## Out of Scope
- List adjacent work, migrations, redesigns, compatibility promises, or operational changes that were explicitly excluded.
```

Append `## Additional Notes` only when useful links or context do not fit the required sections.

## Filing Checklist

Before filing, verify that:

- Every required decision-ledger row is closed without an inferred default.
- The title uses a contract-backed domain identifier and follows the repository issue contract.
- The body contains every required section in order.
- Acceptance criteria and test scenarios are observable and implementation-ready.
- Out-of-scope boundaries are explicit.
- The duplicate search and relevant contract evidence are recorded.
- The create command sets no label or other issue metadata.
