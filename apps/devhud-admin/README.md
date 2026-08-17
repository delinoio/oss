# DevHud Administration

React 19 and Rsbuild single-page administration console. Development is fixed
to `127.0.0.1:46306`; production output is embedded by `devhud-api` and
served at `/admin/`.

Run `pnpm --filter devhud-admin dev` for development and
`pnpm --filter devhud-admin test` for type, component, production-output, and
asset checks.
