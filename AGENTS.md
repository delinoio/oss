### Instructions

- Use the `@docs/` directory as the source of truth. You should list the files in the docs directory before starting any task, and update the documents as required. The `@docs/` directory should always be up-to-date.
- After completing each task, update the relevant documentation in `@docs/` to reflect any changes made.
- Write all code and comments in English.
- Prefer enum types over strings whenever possible.
- If you modified Rust code, run `cargo test` from the root directory before finishing your task.
- If you modified frontend code, run `pnpm test` from the frontend directory before finishing your task.
- Commit your work as frequent as possible using git. Do NOT use `--no-verify` flag.
- Do not guess; rather search for the web.
- Debug by logging. You should write enough logging code.
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.

### Monorepo Structure Map

- `docs/`: Source of truth for project contracts and repository policies.
- `apps/`: User-facing apps (Next.js and React Native).
- `crates/`: Rust crates and Rust-based tooling.
- `cmds/`: Go command tools for workflow orchestration.
- `cli/`: Reserved domain for future standalone CLI tools.
- `servers/`: Backend services and APIs.

### Project Domain Ownership

- `nodeup` -> `crates/nodeup`
- `derun` -> `cmds/derun`
- `mpapp` -> `apps/mpapp`
- `devkit` -> `apps/devkit`
- `devkit-commit-tracker` -> `apps/devkit/src/apps/commit-tracker`
- `devkit-remote-camera` -> `apps/devkit/src/apps/remote-camera`
- `thenv` -> `cmds/thenv`, `servers/thenv`, `apps/devkit/src/apps/thenv`

### Documentation-First Policy

- New project creation requires `docs/project-<id>.md` before runtime implementation.
- Every structural change to project paths must update the corresponding `docs/project-*.md` in the same change.
- New projects must also update `docs/project-monorepo.md` canonical map.
- Domain-level `AGENTS.md` files must remain aligned with `docs/` contracts.

### Project Bootstrap Rules

- Use lowercase kebab-case identifiers for projects and mini apps.
- Keep Devkit mini-app directories at `apps/devkit/src/apps/<id>`.
- Keep Devkit mini-app routes at `/apps/<id>`.
- Keep thenv component mapping stable:
- `cli` -> `cmds/thenv`
- `server` -> `servers/thenv`
- `web-console` -> `apps/devkit/src/apps/thenv`
