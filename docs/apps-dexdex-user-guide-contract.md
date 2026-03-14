# apps-dexdex-user-guide-contract

## Scope
- Project/component: DexDex end-user workflow contract
- Canonical path: `apps/dexdex`
- Contract role: user-facing operational flow and expected behavior boundaries

## Runtime and Language
- Runtime: Tauri desktop/mobile client on Connect RPC APIs
- Primary language: English product UI and workflow documentation

## Users and Operators
- End users creating tasks and managing PR remediation
- Support and enablement teams documenting operational guidance
- QA engineers validating critical workflow paths

## Interfaces and Contracts
User workflow sequence:
1. create/select workspace
2. add repositories and create ordered repository groups
3. create UnitTask
4. monitor SubTask and AgentSession execution
5. use multi-tab workflows for parallel triage
6. stop running UnitTask/SubTask when needed
7. resolve action badges and required actions
8. process plan-mode decisions
9. create PR after diff approval
10. manage tracked PRs and remediation
11. use review assist and inline comments
12. process notifications and deep links

Mandatory behavior contracts:
- UnitTask execution is repository-group scoped.
- Repository order affects execution directory mapping.
- `Cmd+Enter` submits multiline forms.
- stop actions transition to `CANCELLED` through stream updates.
- PR creation and commit-to-local depend on real commit chains.

## Storage
- User-facing workflow state is backed by workspace-scoped task/session/review records.
- Draft and tab restoration behavior is persisted per workspace in client state.

## Security
- Workspace auth and permissions gate user actions.
- Notification deep links and review actions must remain workspace-scoped.

## Logging
- Log user-triggered remediation and stop actions for auditability.
- Log workflow failures with actionable recovery context.

## Build and Test
- `cd apps/dexdex && pnpm test`
- Scenario validations:
  - workspace creation/switching
  - unit-task creation and execution monitoring
  - plan decision flow
  - PR creation/remediation flow
  - inline comment lifecycle

## Dependencies and Integrations
- Base app contract: `docs/apps-dexdex-desktop-app-foundation.md`
- UI contract: `docs/apps-dexdex-ui-contract.md`
- PR contract: `docs/servers-dexdex-pr-management-contract.md`
- Plan contract: `docs/protos-dexdex-plan-mode-contract.md`

## Change Triggers
- Any user-visible workflow, action ordering, or shortcut guidance change must update this doc in the same change as the corresponding UI/app contract.

## References
- `docs/project-dexdex.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/apps-dexdex-ui-contract.md`
