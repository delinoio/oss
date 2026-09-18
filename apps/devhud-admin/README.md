# DevHud Administration

React 19 and Rsbuild single-page administration console. Development is fixed
to `localhost:46306`; production output is embedded by `devhud-api` and
served at `/admin/`.

Use `pnpm exec vp run dev` for authorized team development or `pnpm exec vp run dev:oss` for the
public-contributor environment. The package wrapper receives only the validated
development issuer, proves it matches the API wrapper's pinned preflight
comparison at launch, and starts on the fixed port; it does not inherit API
configuration from Vite Task. For a contributor-owned Logto instance, copy the
package-local `.env.example` to `.env`, use the exact same issuer in
`servers/devhud-api/.env`, and keep both files uncommitted.

OSS mode does not create a shared administrator, user, or application.
Administrator sign-in therefore requires contributor-owned Logto setup or the
authorized team path. Run `pnpm exec vp run devhud-admin#typecheck`, `lint`,
`test:unit`, `test:components`, `test:accessibility`, and `build:frontend` for
the split CI contracts. Run `pnpm exec vp run devhud-admin#build:embedded` or
`verify:embedded` to build
the generated API client, produce the ignored administrator bundle for Go
embedding, and validate its production structure. Run
`pnpm exec vp run devhud-admin#test` for type, component, production-output, and
fixed-port checks. Internal environment ownership is defined in
`docs/repository-environment-contract.md`.

Public guidance is available at the stable `/devhud/admin`,
`/devhud/security`, and `/devhud/support` routes of the configured public
documentation site.
