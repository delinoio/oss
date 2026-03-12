# Feature: interfaces

## Interfaces
Canonical mini app IDs:

```ts
enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
  RemoteFilePicker = "remote-file-picker",
  Thenv = "thenv",
}
```

Routing contract:

```txt
/apps/<id>
```

Shell navigation contract:
- Includes `Home` route (`/`) and all mini app routes.
- Uses route-aware active state (`aria-current="page"`) for the current page.
- Keeps mini app link entries sourced from enum-backed registration (`MINI_APP_REGISTRATIONS`).
- Mobile drawer keeps navigation links out of keyboard tab order while closed.

Mini app directory contract:

```txt
apps/devkit/src/apps/<id>
```

Mini app registration contract (conceptual):
- `id` (enum-style stable identifier)
- `title`
- `route`
- `status` (`placeholder` or `live`)
- `integrationMode` (`shell-only` or `backend-coupled`)

Backend-coupled mini app example:
- `commit-tracker` route is live as an operational dashboard backed by Devkit proxy routes and `servers/commit-tracker` Connect RPC endpoints.
- `remote-file-picker` route is implemented for Phase 1 signed URL uploads (local file/mobile camera) with callback return bridge behavior.
- `thenv` route is implemented as metadata management UI backed by Devkit API proxy routes to `servers/thenv` Connect RPC endpoints.
- Devkit shell remains the owner of global auth/session/navigation concerns.

