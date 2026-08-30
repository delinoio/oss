# DevHud API

Go Connect RPC service for DevHud Bootstrap, Settings, Upload, Account,
Administration, Diagnostics, embedded migrations, and administrator assets. Its
development listener is fixed at `127.0.0.1:46307`.

Use `pnpm dev` for authorized team development or `pnpm dev:oss` for the
public-contributor environment. The package owns local validation, migration,
and serve wrappers; root scripts only orchestrate their order. The team wrapper
accepts the documented API allowlist and rejects missing, unknown, invalid, or
partial upload configuration before migration, including raw asset paths hidden
by parser normalization. Team preflight requires its issuer to match the
administrator wrapper, then pins that comparison through migration and serve.

OSS mode provides PostgreSQL and Logto loopback dependencies and a private
checkout-local identity key. Startup is exclusive per checkout and repairs a
missing Logto database in a preserved PostgreSQL volume before seeding. It
deliberately leaves official R2/Cloudflare uploads unavailable. Guest/bootstrap
behavior works immediately, while authenticated flows require contributor-owned
Logto applications. Copy
`.env.example` to the ignored `.env` only to supply the public identifiers from
those applications. If the issuer is overridden, use the exact same value in
`apps/devhud-admin/.env`; never put GitHub PATs, BYO R2 credentials, or
production, deployment, release, or signing secrets there.

Run `pnpm --filter @delinoio/devhud-api migrate:local` only inside a selected
root development mode. The local API wrapper generates and validates the
ignored administrator bundle before migration or serving. Before direct Go
commands, run `pnpm --filter devhud-admin build:embedded`; then run Go tests
with `go test ./servers/devhud-api/...`. The full internal ownership and
validation contract is `docs/repository-environment-contract.md`.
