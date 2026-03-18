# apps-dexdex-ui-contract

## Scope
- Project/component: DexDex UI and interaction contract
- Canonical path: `apps/dexdex`
- Contract role: cross-screen layout, action semantics, and keyboard behavior

## Runtime and Language
- Runtime: React UI rendered in Tauri webview
- Primary language: TypeScript + CSS design tokens

## Users and Operators
- End users triaging tasks, PR signals, and review activity
- Product and frontend engineers implementing interaction flows
- QA and operators validating accessibility and shortcut parity

## Interfaces and Contracts
Core UX goals:
- triage-first interface for tasks and PR workflows
- persistent AI timeline visibility
- one-action remediation for review/CI issues
- shared desktop/mobile mental model

Layout and navigation contracts:
- desktop layout supports workspace rail, tab bar, list pane, detail pane
- mobile layout uses segmented navigation and stacked detail surfaces
- multi-tab workspace supports open/reorder/close with draft preservation
- repository administration uses dedicated sidebar destinations (`Repository Groups`, `Repositories`) instead of Settings-internal tabs

Primary screens:
- Workspace Home
- UnitTask Detail
- PR Management
- PR Review Assist
- Repository Groups
- Repositories
- Settings
- Notifications Center

Task and PR interaction contracts:
- UnitTask detail includes timeline, logs, diff, commit-chain, and stop controls
- Create Task uses a unified repository-target selector (Repository Groups + Repositories).
- Repository-target selection by repository ID maps to a system-managed singleton group on the server.
- System-managed singleton groups are hidden from Repository Groups management UI and shown as repository labels in task metadata surfaces.
- AI diff approval gates `Create PR` action
- PR review includes line-level inline comments anchored by file/side/line
- unresolved inline-comment count is surfaced in summary contexts

Keyboard contracts:
- global navigation shortcuts (`Cmd+K`, `Cmd+N`, `Cmd+1..3`, `Cmd+,`)
- tab lifecycle shortcuts (`Cmd+T`, `Cmd+W`, `Cmd+Shift+[`, `Cmd+Shift+]`)
- list navigation shortcuts (`J`, `K`, `Enter`, `Cmd+Enter`)
- multiline submit shortcut (`Cmd+Enter`) with IME-safe behavior
- decision shortcuts for plan mode (`A`, `V`, `Shift+X`)

Accessibility and responsive contracts:
- keyboard-first operation
- semantic structure for assistive technologies
- reduced-motion support
- responsive breakpoints for 3-pane, 2-pane, and stacked layouts

## Storage
- workspace-scoped tab and draft state
- shortcut registry and discoverability metadata
- per-workspace badge and preference mappings

## Security
- UI renders only authorized workspace data from normalized API contracts.
- Deep links and inline comment actions must validate workspace/task scope.

## Logging
- log shortcut dispatch and collision-resolution outcomes
- log critical action execution path (`Create PR`, stop actions, plan decisions)
- log UI error/empty-state recovery actions

## Build and Test
- `cd apps/dexdex && pnpm test`
- `cd apps/dexdex && pnpm build`

## Dependencies and Integrations
- Base app contract: `docs/apps-dexdex-desktop-app-foundation.md`
- API/entity contracts:
  - `docs/protos-dexdex-api-contract.md`
  - `docs/protos-dexdex-entities-contract.md`
- Plan, PR, and notification docs:
  - `docs/protos-dexdex-plan-mode-contract.md`
  - `docs/servers-dexdex-pr-management-contract.md`
  - `docs/apps-dexdex-notification-contract.md`

## Change Triggers
- Any screen IA, shortcut semantics, or action-state behavior change must update this document and `docs/apps-dexdex-desktop-app-foundation.md` in the same change.
- Any UI behavior coupled to API/entity updates must synchronize with proto/server contracts.

## References
- `docs/project-dexdex.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/apps-dexdex-user-guide-contract.md`
- `docs/protos-dexdex-entities-contract.md`
