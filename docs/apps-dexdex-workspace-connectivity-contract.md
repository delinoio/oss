# apps-dexdex-workspace-connectivity-contract

## Scope
- Project/component: DexDex workspace connectivity contract
- Canonical path: `apps/dexdex`
- Contract role: connectivity model and setup behavior across desktop and mobile

## Runtime and Language
- Runtime: client workspace profile management over Connect RPC endpoints
- Primary language: TypeScript (workspace setup UX and API integration)

## Users and Operators
- End users configuring local or remote workspaces
- Frontend engineers implementing workspace switching and validation
- Operators documenting endpoint and auth setup expectations

## Interfaces and Contracts
Connectivity types:
- `LOCAL_ENDPOINT`
  - endpoint on same machine/device
  - typical loopback URL such as `http://127.0.0.1:<port>`
- `REMOTE_ENDPOINT`
  - network-hosted endpoint

Shared behavior across connectivity types:
- same Connect RPC service set
- same event-stream contract
- same task/PR/review workflows
- same notification model

Expected differences:
- network latency and availability profile
- auth strictness (optional in local solo, required in shared remote)
- collaboration expectations

Workspace setup flow:
1. enter workspace name
2. choose connectivity type
3. enter endpoint URL
4. verify connectivity
5. save and activate workspace profile

Mobile parity contract:
- mobile uses same workspace model and contracts
- feature rollout is phased by interaction constraints, not by platform priority

## Storage
- workspace profile records (name, type, endpoint, auth profile)
- active workspace pointer
- per-workspace stream checkpoint and tab state

## Security
- remote workspaces require explicit auth handling
- endpoint validation and scope checks are required before activation

## Logging
- connectivity verification outcomes
- workspace switch events
- auth/session refresh issues affecting workspace routing

## Build and Test
- `cd apps/dexdex && pnpm test`
- required scenarios:
  - create local workspace
  - create remote workspace
  - switch workspace and verify cache isolation
  - reconnect stream after endpoint restart/network interruption

## Dependencies and Integrations
- Base app contract: `docs/apps-dexdex-desktop-app-foundation.md`
- API contract: `docs/protos-dexdex-api-contract.md`
- Stream contract: `docs/servers-dexdex-event-streaming-contract.md`

## Change Triggers
- Any workspace connectivity type/flow/auth expectation change must update this document and related app/proto/server contracts in the same change.

## References
- `docs/project-dexdex.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/protos-dexdex-api-contract.md`
- `docs/servers-dexdex-event-streaming-contract.md`
