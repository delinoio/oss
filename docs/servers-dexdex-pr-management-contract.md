# servers-dexdex-pr-management-contract

## Scope
- Project/component: DexDex PR management and remediation contract
- Canonical paths:
  - `servers/dexdex-main-server`
  - `servers/dexdex-worker-server`
- Contract role: polling, actionable signal detection, remediation orchestration, and retry guardrails

## Runtime and Language
- Runtime: main-server polling/orchestration + worker execution runtime
- Primary language: Go

## Users and Operators
- End users managing PR remediation workflows
- Backend engineers implementing PR tracking and auto-fix orchestration
- Operators monitoring remediation success/failure patterns

## Interfaces and Contracts
Scope and entities:
- `PullRequestTracking`
- `ReviewAssistItem`
- `ReviewInlineComment`
- remediation SubTasks (`PR_CREATE`, `PR_REVIEW_FIX`, `PR_CI_FIX`)

Polling loop contract:
1. select active PR tracking entries
2. poll provider APIs
3. normalize provider state into `PrStatus`
4. detect deltas and actionable signals
5. persist updates and emit stream events

Actionable signals:
- review requested changes
- unresolved review-thread activity
- CI failed checks
- merge conflict indicators

Manual remediation flow:
- UI triggers `Fix with Agent`
- API call creates remediation SubTask
- worker executes and streams output
- PR state is re-polled and reflected
- running remediation can be stopped via cancellation APIs

Manual PR creation flow:
- diff approval reveals `Create PR`
- action creates SubTask `PR_CREATE` with prompt `Create A PR`
- worker output must include real commits
- PR opens from SubTask commit chain
- tracking record is created for polling lifecycle

Automatic remediation flow:
- policy-enabled tracking entries auto-run remediation on actionable signals
- attempt counters and max-attempt guardrails apply
- repeated failure transitions to manual-review-required behavior

## Storage
- PR tracking snapshots and poll metadata
- persisted PR tracking fields:
  - `pr_tracking_id`, `workspace_id`, `status`
  - `pr_url`, `unit_task_id`
  - `auto_fix_enabled`, `fix_attempt_count`, `max_fix_attempts`
  - `created_at`, `updated_at`
- remediation attempt budget counters
- review assist records and inline-comment states
- links between UnitTask/SubTask and PR tracking records

## Security
- provider API credentials and tokens must be scoped and protected
- permission-denied states must be explicit and user-visible

## Logging
Required structured logs:
- provider poll request/response metadata
- delta detection and actionable signal classification
- auto-fix decision reasons and attempt counters
- remediation SubTask creation and completion/failure IDs

## Build and Test
- `go test ./servers/dexdex-main-server/...`
- `go test ./servers/dexdex-worker-server/...`
- required scenarios:
  - poll and normalize PR states
  - manual fix flow
  - auto-fix cap/cooldown/blocked-state behavior
  - inline comment stream update propagation

## Dependencies and Integrations
- API contract: `docs/protos-dexdex-api-contract.md`
- Entity model: `docs/protos-dexdex-entities-contract.md`
- App UI/user workflows:
  - `docs/apps-dexdex-ui-contract.md`
  - `docs/apps-dexdex-user-guide-contract.md`
- Worker execution contract: `docs/servers-dexdex-worker-server-foundation.md`

## Change Triggers
- Any PR state model, polling rule, remediation behavior, or guardrail update must update this file with API/entity/app contracts in the same change.

## References
- `docs/project-dexdex.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/protos-dexdex-entities-contract.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
