# Feature: architecture

## Architecture
- Platform shell handles layout, navigation, and global providers.
- Platform shell uses a responsive navigation layout:
  - desktop: persistent left sidebar
  - mobile (`max-width: 960px`): hamburger-triggered off-canvas drawer
- Shared UI tokens map to Toss-style foundation colors and typography, then flow to shell and mini-app surfaces.
- Mini apps live under `src/apps/<id>`.
- Static route pages map each mini app to `/apps/<id>`.
- Shared services layer exposes standard platform utilities.
- Enum-based registration lives in `src/lib/mini-app-registry.ts`.
- Shell navigation menu order is fixed as `Home (/)` first, then registered mini apps from `MINI_APP_REGISTRATIONS`.
- Current route maturity mix: `commit-tracker`, `remote-file-picker`, and `thenv` are live.
- Home route platform status messaging reflects live mini-app maturity and must not describe shell-only bootstrap state.
- Backend-coupled mini apps consume backend APIs while preserving shell-owned auth/session/navigation behavior.
- When available, mini apps use React Query for frontend server-state management.
- Connect RPC + React Query integrations use `@connectrpc/connect-query` ([connect-query-es](https://github.com/connectrpc/connect-query-es)).

