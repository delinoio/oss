# Localization ownership contract

## Repository boundary

Labs Site and Labs Docs source, catalogs, routes, generation and CI are owned by `delinoio/apps`. The Labs rules below describe that external integration; use the [destination contract](https://github.com/delinoio/apps/blob/main/docs/apps-labs-localization-contract.md) for current app procedures. This repository retains `scripts/localization` for the TaiRouter console and owns its fixture validation and local authentication. The two repositories maintain separate tooling copies; this migration does not move console catalogs or credentials.

## Ownership and routes

`labs-docs` and `labs-site` own their `i18n/locales.json` registries,
`i18n/messages/<lang>.json` catalogs and `i18n/state.json` source fingerprints.
English (`en`) is the source language. Both apps register Korean (`ko`), Japanese
(`ja`), Simplified Chinese (`zh-Hans`), Spanish (`es`), French (`fr`), German
(`de`) and Brazilian Portuguese (`pt-BR`). English URLs remain unprefixed;
other languages use `/<lang>/`. Language selection is explicit, with no browser
language redirect. Rspress owns documentation navigation, search, language
selection and HTML language metadata. The marketing root and its language variants publish an indexable Delino Labs product landing page with localized catalog text and explicit language selection. The TaiRouter
product page uses `/tairouter/` and `/<lang>/tairouter/`, localized catalog text
and an explicit language menu; its product routes permit indexing.

Documentation source pages live in `apps/labs-docs/docs/en`. Target pages live in
sibling language directories. Only these curated public pages and app catalogs
are translation input. Internal `docs/` contracts are never translation input.
Theme catalog entries retain their MIT attribution and license in
`apps/labs-docs/i18n`; copies in `docs/public` ship with the static build.

The TaiRouter console also consumes this shared tooling. Its catalog, browser behavior and language selection are owned by the [TaiRouter console contract](apps-tairouter-console-contract.md#localization); the Labs route rules above do not apply to console routes.

## Maintainer commands

Run `pnpm i18n:login` to open the system browser and authorize ChatGPT plan usage.
Then run `pnpm i18n:models` to list account-specific models and
`pnpm i18n:translate` to generate missing or source-changed translations.
`DELINO_TRANSLATION_MODEL` selects a slug from that catalog; the default is its
first available model. Root translation runs participating workspaces with a
concurrency of two. Each app permits at most two simultaneous inference requests.
Run from either app to limit work to that app, or pass a target language, for
example `pnpm i18n:translate ko`. Review the generated changes before committing.

`pnpm i18n:check` checks all participating apps offline from the root, or only
the current app when run there. Builds and tests require current translations
but never sign in or call inference. `pnpm i18n:profiles` lists registrations;
`pnpm i18n:login <profile-id>` reauthorizes and selects one;
`pnpm i18n:login --new` adds a registration. `pnpm i18n:logout` revokes the active
renewable session and clears its local tokens while retaining registration IDs.
If remote revocation fails, the CLI reports how to disconnect through ChatGPT.

To add a language, register its BCP 47 tag and native label in both apps, generate
translations, review them and run both app tests. Add source text to catalogs or
English Markdown pages. Do not put translatable text in browser code. New
marketing text requires its own product/design work and catalog integration.

## Generation and validation

`scripts/localization/` owns local authentication, incremental generation and
offline structural validation. It derives from the Apache-2.0 Delino Apps
tooling. Catalog values are batched in groups of at most 40; Markdown pages are
individual units. Completed batches are saved with SHA-256 source fingerprints.
Unchanged translations, including human edits, remain unchanged. A per-checkout
temporary lock rejects overlapping generation for the same app. Failed request
groups save successful peers and stop further requests, so reruns resume work.
Remove obsolete translations and state entries explicitly; generation never
silently deletes them.

Checks reject missing/stale output, extra keys/pages, changed placeholders, code
spans, code fences, link destinations, heading structure or structural frontmatter.
The tool localizes absolute internal Markdown links after validating output.
MDX and symbolic links are rejected. The inherited exact `sourceOnly` policy is
available only for an explicit owning product requirement; Labs Docs declares the eleven migrated `tairouter/*.md` and
`tairouter/guide/*.md` pages by exact path as English-only under the TaiRouter
public documentation contract. This initial publication preserves the complete
English guidance without labelling it as translated content. Every localized
Labs sidebar labels the group as English and links to those unprefixed routes.
All other Labs documentation pages and all marketing catalog messages require
registered translations. Structural checks do not establish linguistic accuracy.

## Authentication and inference

Translation is a local maintainer operation, using the official
[registration](https://developers.openai.com/siwc/token-sharing-open-source/sign-in),
[session](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions)
and [inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)
flows. Eligible ChatGPT plan usage and `chatgpt.tokens.use.direct` consent are
required. No API key or copied Codex credentials are used. A stable installation
host UUID identifies OAuth registration; it is not a persisted business entity.
Sign-in uses dynamic client registration, PKCE, state, nonce, a loopback callback
and verified ID-token signature, issuer, audience and expiration through `jose`.
Credentials remain outside the checkout in `~/.config/delino-translations/` with
owner-only permissions, atomic writes and serialized refresh operations. This
session directory is shared with the reference Delino translation tooling.
Inference releases the auth lock after retrieving the latest token.

Model discovery uses `GET https://api.openai.com/v1/models`. Translation uses
`POST https://api.openai.com/v1/responses` with `store: false` and `stream: true`.
Accept output only after `response.completed`. Refusals, incomplete/interrupted
streams, failed authorization and usage limits fail without marking affected
units complete. The CLI identifies plan usage and links to
`https://chatgpt.com/settings/usage`. Never bundle authentication modules or
tokens into static apps or CI artifacts.

## Acceptance boundaries

Destination app tests run offline localization fixtures and build validation. Shared
tooling changes in this repository select TaiRouter validation only.
Fixture sign-in and inference tests do not establish real account consent, model
discovery, token renewal, revocation or plan-backed generation. Record those
unperformed checks separately from static build success and Cloudflare acceptance.
