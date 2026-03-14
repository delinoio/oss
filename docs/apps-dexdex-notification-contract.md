# apps-dexdex-notification-contract

## Scope
- Project/component: DexDex notification UX contract
- Canonical path: `apps/dexdex`
- Contract role: client notification behavior for stream-driven events

## Runtime and Language
- Runtime: Web Notification API + in-app notification center
- Primary language: TypeScript (client reducer and UI handlers)

## Users and Operators
- End users consuming task/plan/PR/review alerts
- Frontend engineers implementing notification dispatch and dedup
- Operators monitoring notification reliability signals

## Interfaces and Contracts
Notification design rules:
- primary channel: Web Notification API
- authoritative state: in-app notification center
- event source: workspace event stream

Trigger sources:
- UnitTask action-required transitions
- plan approval waits
- PR review activity requiring remediation
- PR CI failures
- agent-session failures
- user-input required states

Flow contract:
- app startup requests notification permission
- event stream dispatches notification payloads
- client writes notification record and conditionally emits Web Notification API event
- deep-link click opens relevant task/PR/review context

Dedup contract:
- dedupe key includes `workspace_id`, stream `sequence`, and `notification_type`

Delivery rules:
- foreground: in-app toast + notification center
- background: Web Notification API + notification center
- no permission: notification center only

## Storage
- permission status cache
- notification read/unread state and timestamps
- dedupe sequence checkpoint metadata

## Security
- notification payloads must exclude secret values
- deep links must remain workspace-scoped and validated

## Logging
Client logs:
- permission prompts/outcomes
- dispatch success/failure
- deep-link routing outcomes

Server-side correlation logs (integration requirement):
- generation reason
- workspace/task/PR identifiers

## Build and Test
- `cd apps/dexdex && pnpm test`
- required scenarios:
  - permission prompt handling
  - foreground/background behavior
  - dedupe correctness
  - deep-link routing

## Dependencies and Integrations
- Entity enums: `docs/protos-dexdex-entities-contract.md`
- Stream contract: `docs/servers-dexdex-event-streaming-contract.md`
- App base contract: `docs/apps-dexdex-desktop-app-foundation.md`

## Change Triggers
- Any notification trigger/category/delivery rule change must update this file and aligned entity/stream docs in the same change.

## References
- `docs/project-dexdex.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-event-streaming-contract.md`
- `docs/protos-dexdex-entities-contract.md`
