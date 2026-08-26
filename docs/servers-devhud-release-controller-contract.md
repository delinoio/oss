# servers-devhud-release-controller-contract

## Scope

This contract defines the operator-provided deployment boundary used by the dedicated DevHud release workflow. The repository does not select or hard-code the official API hosting provider. An operator may implement the boundary on any platform that can honor the exact OIDC, immutable-image, migration, updater, readiness, and rollback semantics below.

## Identity and authentication

The controller base URL is the credential-free HTTPS variable `DEVHUD_RELEASE_CONTROLLER_URL`; `DEVHUD_RELEASE_CONTROLLER_AUDIENCE` is the exact GitHub Actions OIDC audience. Controller jobs request `id-token: write`, exchange the GitHub Actions request token for a short-lived audience-bound token, mask it immediately, and send it as a bearer credential. The controller must restrict that identity to the `delinoio/oss` `release-devhud.yml` workflow on `refs/heads/main`, the protected release environments, and the exact source revision. It must reject redirects, long-lived deployment credentials, mismatched project/version/revision, unknown fields, replay under another release identity, and attempts to mutate an unprepared release.

Every request is schema version 1 and binds project `devhud`, stable version, derived `devhud@v<version>` tag, and 40-hex source revision. Successful mutation and status responses contain `ok: true` plus the same project, version, and revision. Responses and logs must not include environment values, credentials, updater contents, database locators, object-store locators, or provider-private response bodies.

## Endpoints

- `POST v1/devhud/releases/preflight` is read-only. It authenticates deployment authority and validates the selected platform plus PostgreSQL and R2 connectivity/configuration. Its response has `checks.postgresql`, `checks.r2`, and `checks.release-controller` all exactly `true`; omission or any other value fails closed.
- `POST v1/devhud/releases/prepare` accepts the immutable `apiImage` and `sweeperImage` digest references plus an updater input containing its SHA-256 and Base64 bytes. It verifies the digest references and updater digest, stages but does not expose updater manifests, runs the exact embedded migrations, confirms schema readiness, confirms both numeric non-root image users, and confirms that the API and sweeper contain the same administrator assets and migration closure.
- `POST v1/devhud/releases/<encoded-tag>/promote-api` promotes the prepared API and sweeper atomically or leaves the previous pair authoritative. It keeps updater discovery closed. Success requires `/healthz`, `/readyz`, `/admin/`, and Bootstrap to expose the exact release version and project.
- `POST v1/devhud/releases/<encoded-tag>/promote-updater` exposes all ten previously prepared stable signed updater manifests as one release boundary only after the GitHub Release is public. It must never serve a partial target set.
- `GET v1/devhud/releases/<encoded-tag>/status` reports closed channel values. Final verification requires `channels.api`, `channels.sweeper`, and `channels.updater` all equal `public` for the exact identity.
- `POST v1/devhud/releases/<encoded-tag>/rollback` is accepted only before any store package becomes public. It restores the previously recorded API/sweeper pair and keeps or restores the prior updater set. Once a store is public, the controller must reject automatic rollback and require a coordinated roll-forward or emergency withdrawal.

All mutation endpoints are idempotent for an identical release identity and identical immutable inputs. A conflicting retry fails closed rather than replacing prepared state.

## Runtime configuration contract

The controller validates and supplies these stable runtime input names without returning their values:

- Core deployment: `DEVHUD_ENVIRONMENT`, `DEVHUD_DATABASE_URL`, `DEVHUD_PUBLIC_API_URL`, `DEVHUD_LISTEN_ADDRESS`, `DEVHUD_TRUSTED_PROXY_CIDRS`.
- Logto: `DEVHUD_LOGTO_ISSUER`, `DEVHUD_LOGTO_AUDIENCE`, `DEVHUD_LOGTO_DESKTOP_CLIENT_ID`, `DEVHUD_LOGTO_IOS_CLIENT_ID`, `DEVHUD_LOGTO_ANDROID_CLIENT_ID`, `DEVHUD_LOGTO_ADMIN_CLIENT_ID`, `DEVHUD_ADMIN_REDIRECT_URI`.
- Identity and uploads: `DEVHUD_IDENTITY_HMAC_KEYS`, `DEVHUD_R2_ENDPOINT`, `DEVHUD_R2_ACCESS_KEY_ID`, `DEVHUD_R2_SECRET_ACCESS_KEY`, `DEVHUD_R2_STAGING_BUCKET`, `DEVHUD_R2_PUBLIC_BUCKET`, `DEVHUD_PUBLIC_ASSET_BASE_URL`.
- Cloudflare controls: `DEVHUD_CLOUDFLARE_API_TOKEN`, `DEVHUD_CLOUDFLARE_ZONE_ID`, `DEVHUD_CLOUDFLARE_RATE_LIMIT_RULE_ID`.
- Updater and sweeper: `DEVHUD_UPDATE_MANIFEST_DIR`, `DEVHUD_SWEEPER_BATCH_SIZE`, `DEVHUD_SWEEPER_INTERVAL`.

The preflight must fail if a required value is absent, invalid for the production server contract, or cannot authenticate to PostgreSQL, R2, Logto, the public asset domain, or the selected deployment platform. The controller must use the server's documented migration command before promotion and must not manufacture, modify, or sign updater material.

## GitHub environment configuration names

The protected environments are `devhud-private-build`, `devhud-publication`, `devhud-store-review-approved`, `devhud-store-publication`, and `devhud-ga`. The following are stable GitHub Actions configuration names; documentation and dry-run output record names and presence only, never values.

Variables:

- Release destinations: `DEVHUD_APP_STORE_APP_ID`, `DEVHUD_GOOGLE_PLAY_PACKAGE_NAME`, `DEVHUD_GOOGLE_PLAY_MANAGED_PUBLISHING`, `DEVHUD_CHROME_WEB_STORE_PUBLISHER_ID`, `DEVHUD_OCI_REGISTRY`, `DEVHUD_OCI_API_REPOSITORY`, `DEVHUD_OCI_SWEEPER_REPOSITORY`.
- Deployment/readiness: `DEVHUD_RELEASE_CONTROLLER_URL`, `DEVHUD_RELEASE_CONTROLLER_AUDIENCE`, `DEVHUD_PUBLIC_API_URL`, `DEVHUD_LOGTO_ISSUER`, `DEVHUD_PUBLIC_ASSET_BASE_URL`, `DEVHUD_PUBLIC_DOCS_URL`, `DEVHUD_PUBLIC_DOCS_ACCOUNT_ID`, `DEVHUD_PUBLIC_DOCS_PROJECT_NAME`.
- Signing identities and policy: `DEVHUD_CHROME_EXTENSION_ID`, `DEVHUD_MACOS_SIGNING_IDENTITY`, `DEVHUD_WINDOWS_CERTIFICATE_SHA256`, `DEVHUD_WINDOWS_TIMESTAMP_URL`, `DEVHUD_APPLE_TEAM_ID`, `DEVHUD_ANDROID_CERTIFICATE_SHA256`.

Secrets:

- Updater and desktop signing: `DEVHUD_CHROME_EXTENSION_PUBLIC_KEY`, `DEVHUD_UPDATER_SIGNING_KEY_B64`, `DEVHUD_MACOS_DEVELOPER_ID_P12_B64`, `DEVHUD_MACOS_DEVELOPER_ID_P12_PASSWORD`, `DEVHUD_WINDOWS_SIGNING_PFX_B64`, `DEVHUD_WINDOWS_SIGNING_PFX_PASSWORD`.
- Apple signing and store access: `APPLE_API_ISSUER`, `APPLE_API_KEY_ID`, `APPLE_API_PRIVATE_KEY_B64`, `DEVHUD_IOS_DISTRIBUTION_P12_B64`, `DEVHUD_IOS_DISTRIBUTION_P12_PASSWORD`, `DEVHUD_IOS_APP_PROFILE_B64`, `DEVHUD_IOS_WIDGET_PROFILE_B64`, `DEVHUD_IOS_WIDGET_INTENT_PROFILE_B64`.
- Android signing and Play access: `DEVHUD_ANDROID_UPLOAD_KEYSTORE_B64`, `DEVHUD_ANDROID_KEYSTORE_PASSWORD`, `DEVHUD_ANDROID_KEY_ALIAS`, `DEVHUD_ANDROID_KEY_PASSWORD`, `DEVHUD_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON`.
- Chrome Web Store: `DEVHUD_CHROME_WEB_STORE_CLIENT_ID`, `DEVHUD_CHROME_WEB_STORE_CLIENT_SECRET`, `DEVHUD_CHROME_WEB_STORE_REFRESH_TOKEN`.
- OCI and documentation publication: `DEVHUD_OCI_REGISTRY_USERNAME`, `DEVHUD_OCI_REGISTRY_TOKEN`, `DEVHUD_PUBLIC_DOCS_API_TOKEN`.

`GITHUB_TOKEN` is the workflow-scoped GitHub credential. `DEVHUD_RELEASE_CONTROLLER_TOKEN` is minted from OIDC within each controller-facing job and is never stored as a secret. No stable configuration value may be printed, uploaded, copied into release evidence, or passed to static/dry tests.

## Release boundaries

Infrastructure preparation occurs only after Apple, Google Play, and Chrome review is independently approved and held. API/sweeper promotion precedes store publication, but updater discovery remains closed. Store publication, regular GitHub Release publication, updater exposure, and public-doc deployment are serialized. No GA state or announcement is permitted until independent store queries and final controller status confirm every channel public.

## Validation

`scripts/release/devhud-release-controller.test.mjs` validates deterministic request binding, updater digests, and fail-closed response identity. `scripts/release/devhud-public-release.test.mjs` validates state ordering and rollback boundaries. These tests use static/dry fixtures and never call a controller or publication service.
