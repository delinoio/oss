### Instructions for `apps/`

- Follow root `AGENTS.md` and project-specific docs before adding or changing app code.
- Keep app-specific contracts synchronized in `docs/project-*.md` in the same change.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Follow Toss Design Guidelines for frontend UX/UI decisions across web and mobile apps.

### Scope in This Domain

- `apps/devkit`: Next.js 16 micro-app platform.
- `apps/mpapp`: Expo React Native mobile app.

### Devkit Identifier Contract

Treat Devkit mini app IDs as stable enum-style values:

```ts
enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
  RemoteFilePicker = "remote-file-picker",
  Thenv = "thenv",
}
```

### Devkit Rules

- Mini app code must live at `apps/devkit/src/apps/<id>`.
- Mini app identifiers must be stable kebab-case values.
- Mini app routes must follow `/apps/<id>`.
- Shared shell concerns belong to Devkit platform modules, not mini app internals.
- New mini apps require a `docs/project-devkit-<id>.md` document before implementation.

### mpapp Rules

- `mpapp` must remain Expo-based unless a documented architecture decision changes it.
- Bluetooth capabilities and permissions must be explicitly documented in `docs/project-mpapp.md`.

### Testing and Validation

- If frontend code changes in this domain, run `pnpm test` before finishing.
- Update relevant docs in `docs/` for every behavior, structure, or interface change.
