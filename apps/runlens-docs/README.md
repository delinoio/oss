# Runlens documentation

Public guides: https://runlens.delino.io.

Run `pnpm dev:runlens-docs` at the repository root or `pnpm dev` here. Development binds to `127.0.0.1:46310`; preview uses `127.0.0.1:46272`. Conflicts fail without remapping. Run `pnpm test` to build and validate every public route, installer copy, and content boundary.

Cloudflare Pages: build from the repository root with `pnpm --filter runlens-docs build`; output is `apps/runlens-docs/doc_build`. Credentials and DNS configuration are external. These files do not publish the site.

Internal source contracts: [project](../../docs/project-runlens.md), [app](../../docs/apps-runlens-docs-foundation.md).
