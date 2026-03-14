# protos-dexdex-plan-mode-contract

## Scope
- Project/component: DexDex plan-mode interaction contract
- Canonical source path: `protos/dexdex/v1/dexdex.proto`
- Contract role: define decision gating between user and agent execution at SubTask scope

## Runtime and Language
- Runtime: Connect RPC orchestration + event streaming across app/main-server/worker-server
- Primary language: Protocol Buffers (`proto3`) + enum-based decision semantics

## Users and Operators
- End users approving or revising proposed plans
- Client engineers implementing plan decision UX and keyboard flow
- Main-server engineers validating state transitions
- Worker engineers handling pause/resume execution checkpoints

## Interfaces and Contracts
Core model:
- Plan mode applies at SubTask execution level.
- Plan proposal checkpoints are emitted during AgentSession output.
- Execution pauses at explicit decision boundaries.

Decision actions:
- `APPROVE`: continue execution with current plan.
- `REVISE`: continue execution with feedback.
- `REJECT`: terminate current execution path and return control.

State contracts:
- SubTask plan states: `WAITING_FOR_PLAN_APPROVAL`, `IN_PROGRESS`, `COMPLETED`, `FAILED`, `CANCELLED`
- AgentSession plan states: `RUNNING`, `WAITING_FOR_INPUT`

RPC contract:
- Decision endpoint: `TaskService.SubmitPlanDecision`
- Request fields:
  - `sub_task_id`
  - `decision`
  - optional revision/reason field depending on selected decision

Event-stream contract:
- Plan proposal payloads are emitted via `SESSION_OUTPUT` events.
- State changes are emitted via `SUBTASK_UPDATED` and `SESSION_STATE_CHANGED` events.

UI behavior contract:
- While waiting for plan approval, show `Approve`, `Revise`, and `Reject` controls.
- Preserve decision audit trail in session timeline.
- Revise input uses multiline submit shortcut (`Cmd+Enter`).
- Decision shortcuts must work regardless of IME language mode.

## Storage
Persist plan decision records with:
- decision type
- optional feedback text
- decision timestamp
- acting user ID
- linked `session_id` and `sub_task_id`

## Security
- Decision RPCs require workspace-scoped authorization.
- Audit records must preserve actor identity and decision intent.

## Logging
- Log decision receipts, validation outcomes, and state transitions.
- Log failed transitions with `FAILED_PRECONDITION` mapping details.

## Build and Test
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- `cd apps/dexdex && pnpm test`

## Dependencies and Integrations
- API contract: `docs/protos-dexdex-api-contract.md`
- Entity model: `docs/protos-dexdex-entities-contract.md`
- App UX contracts:
  - `docs/apps-dexdex-desktop-app-foundation.md`
  - `docs/apps-dexdex-ui-contract.md`
- Server execution contracts:
  - `docs/servers-dexdex-main-server-foundation.md`
  - `docs/servers-dexdex-worker-server-foundation.md`

## Change Triggers
- Any plan decision enum/state/RPC/event change must update this document and `docs/protos-dexdex-v1-contract.md` in the same change.
- Any plan UX behavior change must update app-domain contracts in the same change.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-v1-contract.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/protos-dexdex-entities-contract.md`
