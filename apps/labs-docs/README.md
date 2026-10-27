# Labs Docs

Static Rspress documentation for `https://docs.labs.delino.io`.

Use Node.js 24 and pnpm 10.26.2. From the repository root:

```sh
pnpm install
pnpm dev:labs-docs
pnpm --filter labs-docs test
pnpm --filter labs-docs build
pnpm --filter labs-docs preview
```

Development binds to `127.0.0.1:46303`; preview binds to `127.0.0.1:46304`. Port conflicts fail. Edit pages in `docs/`; the production output is `doc_build/`.

See the [project index](../../docs/project-labs-docs.md) and [app contract](../../docs/apps-labs-docs-foundation.md) for route ownership, validation and Cloudflare Pages settings. Hosting and DNS provisioning are separate operator steps.
