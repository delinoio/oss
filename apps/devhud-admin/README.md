# DevHud Administration

React 19 and Rsbuild single-page administration console. Development is fixed
to `localhost:46306`; production output is embedded by `devhud-api` and
served at `/admin/`.

Use `pnpm dev` for authorized team development or `pnpm dev:oss` for the
public-contributor environment. The package wrapper receives only the validated
development issuer, proves it matches the API wrapper's pinned preflight
comparison at launch, and starts on the fixed port; it does not inherit API
configuration from Turbo. For a contributor-owned Logto instance, copy the
package-local `.env.example` to `.env`, use the exact same issuer in
`servers/devhud-api/.env`, and keep both files uncommitted.

OSS mode does not create a shared administrator, user, or application.
Administrator sign-in therefore requires contributor-owned Logto setup or the
authorized team path. Run `pnpm --filter devhud-admin test` for type, component,
production-output, and fixed-port checks. Internal environment ownership is
defined in `docs/repository-environment-contract.md`.

Public guidance is available at the stable `/devhud/admin`,
`/devhud/security`, and `/devhud/support` routes of the configured public
documentation site.
