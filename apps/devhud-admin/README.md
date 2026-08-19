# DevHud Administration

React 19 and Rsbuild single-page administration console. Development is fixed
to `localhost:46306`; production output is embedded by `devhud-api` and
served at `/admin/`.

Set `DEVHUD_LOGTO_ISSUER` to the same issuer configured on the development API,
then run `pnpm --filter devhud-admin dev`. The development CSP permits that
validated issuer origin for Logto discovery and token requests. Run
`pnpm --filter devhud-admin test` for type, component, production-output, and
asset checks.
