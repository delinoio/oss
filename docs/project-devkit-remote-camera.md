# Project: devkit-remote-camera

## Goal
`devkit-remote-camera` is a Devkit mini app for remote camera control and live camera workflow tasks.
It provides a focused control surface within Devkit.

## Path
- `apps/devkit/src/apps/remote-camera`

## Runtime and Language
- Next.js 16 mini app module (TypeScript)

## Users
- Operators who need remote camera control from web
- Team members monitoring camera sessions

## In Scope
- Camera session list and session detail control UI
- Core camera actions in web UI
- Integration with backend control/status endpoints
- Live status indicators for camera availability

## Out of Scope
- Full media asset management suite
- Advanced post-processing workflows
- Device firmware management

## Architecture
- Remote camera UI module for control and status.
- Data/control adapter for backend API integration.
- Shared Devkit shell integration for routing and navigation.

## Interfaces
Canonical mini app identifier:

```ts
enum MiniAppId {
  RemoteCamera = "remote-camera",
}
```

Route contract:
- `/apps/remote-camera`

Conceptual control contract:
- Session list endpoint
- Session status endpoint
- Camera action command endpoint

## Storage
- Client-side state for selected session and UI preferences.
- Session/control authority belongs to backend service.
- No standalone mini app database.

## Security
- Apply strict authorization checks for camera control actions.
- Log privileged actions for audit trails.
- Prevent exposing sensitive camera endpoint details in client logs.

## Logging
Required baseline logs:
- Control action request/result
- Session status polling/stream errors
- Route and render failures

## Build and Test
Planned commands:
- `pnpm --filter devkit... test`
- Module-focused tests for remote camera features when scaffolding is available.

## Roadmap
- Phase 1: Session listing and basic control actions.
- Phase 2: Live status improvements and action reliability.
- Phase 3: Multi-camera coordination and operational dashboards.

## Open Questions
- Real-time transport mechanism and reconnection policy.
- Required action audit field set for compliance.

## References
- `docs/project-template.md`
- `docs/project-monorepo.md`
- `docs/project-devkit.md`
