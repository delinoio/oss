# DeliDev app

English-only responsive React PWA for the future `https://deli.dev` origin.
This directory produces an artifact for Cloudflare Pages; its configuration
does not create, activate, or deploy a Pages project.

## Local setup

```sh
cp .env.example .env
pnpm install
pnpm --filter @delinoio/delibase-connect build
pnpm --filter delidev-app dev
```

Only browser-safe values belong in `.env`. Never add Logto client secrets,
access tokens, Polar secrets, or invitation tokens.

## Validation

```sh
pnpm --filter delidev-app typecheck
pnpm --filter delidev-app lint
pnpm --filter delidev-app test
pnpm --filter delidev-app build
pnpm --filter delidev-app test:pwa
pnpm --filter delidev-app test:browser
```

The production output is `dist`. Public catalog reads use the anonymous
`CatalogService` transport. Every protected call obtains a Logto token for
`https://delibase.deli.dev`; `UsageService` is intentionally absent from the
browser application.

The service worker stores only its generated, versioned shell allowlist and
anonymous public catalog responses. It does not persist account, invitation,
organization, team, balance, ledger, usage, or token data.
