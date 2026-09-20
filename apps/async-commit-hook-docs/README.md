# ach documentation

Public user documentation at https://ach.delino.io. The local results UI ships inside `ach`.

- `pnpm dev` (or root `pnpm dev:async-commit-hook-docs`): fixed loopback port 46310.
- `pnpm build`: Rspress output in ignored `doc_build`.
- `pnpm preview`: fixed loopback port 46281.
- `pnpm test`: build, routes/content/installers and fixed-port checks.

Public installers are maintained in `public`; the release builder copies them unchanged. The manual release workflow publishes this app's `doc_build` to the existing Cloudflare Pages project. See the internal app contract for ownership and migration details.
