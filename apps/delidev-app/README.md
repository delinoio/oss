# DeliDev app

English-only responsive React PWA for the future `https://deli.dev` origin.
This directory produces an artifact for Cloudflare Pages; its configuration
does not create, activate, or deploy a Pages project.

## Local setup

```sh
cp .env.example .env
pnpm install
pnpm --filter @delinoio/delibase-connect build
pnpm --filter @delinoio/devhud-deck-connect build
pnpm --filter delidev-app dev
```

Only browser-safe values belong in `.env`. Never add Logto client secrets,
access tokens, Polar secrets, or invitation tokens.

Deck connection management additionally requires the browser-safe Deck origin,
audience, GitHub App client ID/slug, and exact callback URI shown in
`.env.example`. These values are identifiers, not provider credentials. If a
Deck value is absent or invalid, only the Deck controls are disabled.

Artifact builds also require `DELIDEV_DEVHUD_APPLE_TEAM_ID` and
`DELIDEV_DEVHUD_ANDROID_CERTIFICATE_SHA256`. They are public verified-link
identities used to generate the exact-path DevHud association files. CI uses
explicit test-only identities; release builds must inject the actual Apple Team
ID and Android release-certificate SHA-256 fingerprint.

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
browser application. The settings surfaces use a separate, procedure-restricted
Deck transport for `DeckIntegrationService` only.

Logto access, refresh, and ID tokens remain in memory. Same-tab session storage
is limited to PKCE state, the one-shot protected return path consumed by the
authentication callback, and a non-sensitive one-shot Deck settings return
path. Deck OAuth state remains server-side.

The service worker stores only its generated, versioned shell allowlist and
anonymous public catalog responses. It does not persist account, invitation,
organization, team, balance, ledger, usage, or token data.
