# Repository environment contract

## Configuration ownership

Repository files contain public configuration, toolchain selectors, names and safe examples. Real `.env` files, `.infisical.json`, `.dev-environment/`, user credentials, and production, release and signing credentials remain uncommitted. Do not add a root environment example. Service-local `.env.example` files contain names, validation guidance, placeholders and safe loopback defaults only.

User-owned credentials stay in the owning local mechanism. They must not enter repository files, logs, process arguments or shared build cache metadata. Secrets belong to their owning service or protected release environment.

## Local development

Use `pnpm dev:labs-docs` for the independent Labs site at `127.0.0.1:46303`. Package preview uses `127.0.0.1:46304`. Port conflicts fail; no secret or environment input is required. Follow [the Labs app contract](apps-labs-docs-foundation.md).

Use `pnpm dev:public-docs` for the consolidated documentation site at `127.0.0.1:46302`. Port conflicts fail instead of selecting another port. Follow `apps-public-docs-foundation.md` for wrapper and shutdown behavior.

Use `pnpm dev:async-commit-hook` for the application frontend. Its development frontend uses fixed port `46308`; the local UI/API uses configurable `api_port`, default `46309`. No root team or OSS dependency orchestrator is required.

The shared `scripts/dev-environment/process.mjs` helper retains argv-based command invocation and bounded platform/tool environment construction for development wrappers. Spawn children with explicit argument arrays and `shell: false`. Windows pnpm shims use explicit `cmd.exe` invocation. Forward termination signals and wait for process trees to exit.

Use `pnpm dev:labs-site` for the static marketing site at `127.0.0.1:46312`. The app uses the shared strict-port development wrapper and requires no service secrets. See [the labs-site foundation](apps-labs-site-foundation.md).

## Native CLI repository publication

The `linux-packages` GitHub Environment owns only the package signing subkey/passphrase and the two-bucket R2 object credentials listed in `repository-linux-packages-contract.md`. Its non-secret account ID and public fingerprint are Environment variables. Main and the seven exact project tag namespaces (including `clibox@v*`) may enter this boundary; pull requests and dry runs never receive these secrets. These values are unrelated to local service configuration, Infisical, Turbo development environments. Primary signing recovery material is operator-owned outside the repository and CI.

The async-commit-hook local UI/API remain on configurable api_port (default 46309), with frontend development on fixed 46308. Its public documentation is served by the consolidated `public-docs` app on fixed development port 46302; the root `pnpm dev:public-docs` command never invokes Infisical. No new environment input is required.

Root `pnpm dev:aiprouter` starts a self-contained local evaluation through `scripts/aiprouter/local.mjs` and `packaging/aiprouter/compose.local.yml`. It requires Node.js 24, the pinned pnpm, and a running Docker daemon with Compose; host Go, workspace dependency installation, and external service accounts are not required. The launcher builds the console/API and synthetic OIDC/SMTP service, starts PostgreSQL, waits for fixture readiness, runs migrations with admission stopped, and starts the API with its bundled console at `http://127.0.0.1:8080`. Sign-in uses the synthetic Alice identity without a password; Alice is a local instance operator. Only loopback ports 8080 and 18090 are published; conflicts fail.

The local launcher generates random database credentials and a versioned 32-byte encryption key once in owner-readable `.dev-environment/aiprouter/.env`. Repeated starts retain these values and PostgreSQL state. Each checkout has a separate Compose project derived from its absolute path. Deployment `.env` files and exported application credentials are not loaded. If the local environment file is missing while project volumes remain, startup refuses to replace lost encryption keys. Keep that file with its database; it must not be committed or copied into images. Local SMTP discards content. No real provider is preconfigured; R2 uses explicitly nonfunctional placeholders, so generated image/video storage requires real R2 configuration. The fixture identity provider is for local evaluation only.

Startup returns after readiness; containers continue in the background. `pnpm dev:aiprouter:logs` follows logs. `pnpm dev:aiprouter:stop` removes only this checkout's local containers/network and retains its database volume and environment file. Startup failures after provisioning stop local services and preserve state. The launcher never starts production deployment or observability containers. `pnpm dev:public-docs` remains separate.

For source development with frontend reload, `pnpm dev:aiprouter:source` delegates to Turborepo to start the Go API, React console, and public documentation server together. It requires the pinned Node.js/pnpm versions, the Go toolchain, installed workspace dependencies, and configured PostgreSQL, OIDC, SMTP, and R2 services. It does not start deployment or observability containers, migrate databases, or seed user state. Prepare a disposable development database with the existing server `migrate` command before starting admission.

The API workspace uses Node.js `--env-file-if-exists=.env --run dev:serve` to read optional `servers/aiprouter/.env` before running Go. Exported environment variables take precedence. File values are passed only to the API child; they are not loaded into frontend environments. Turbo uses strict environment filtering. The API package configuration passes through `AIPROUTER_*`, `OTEL_*`, and the declared Go/compiler and certificate variables only to the API task. Use the configuration names and validation requirements in the [operations contract](servers-aiprouter-operations-contract.md); the service-local example contains placeholders, not working credentials. Keep populated files uncommitted and owner-readable only.

Use `AIPROUTER_LISTEN_ADDR=127.0.0.1:8080` and `AIPROUTER_PUBLIC_URL=http://127.0.0.1:4173`. Register `http://127.0.0.1:4173/auth/callback` with the development OIDC provider. The console forwards `/auth` and Connect RPC traffic to the API at port 8080. The existing synthetic OIDC fixture accepts only the API-origin callback at port 8080 and cannot replace an OIDC provider registered for port 4173 without changes. The console binds to fixed loopback port 4173 and fails on conflicts. Public docs use the consolidated loopback port 46302. API overrides must remain compatible with the console proxy.

The API task runs `go run .` within `servers/aiprouter`; restart it after Go source changes. Web servers retain their normal development reload. Turborepo prefixes task logs with their workspace names and owns concurrent execution, failure handling, and termination. Development tasks are persistent and uncached. Use `Ctrl+C` to stop the task group. A failed task causes the default Turbo run to fail and cancel running tasks.

## Google OAuth deployment configuration

Google Antigravity OAuth login, completion and refresh require both `AIPROUTER_GOOGLE_OAUTH_CLIENT_ID` and `AIPROUTER_GOOGLE_OAUTH_CLIENT_SECRET` from the service environment. No upstream installed-application credentials are embedded. Missing values refuse these exchanges before network access; other provider paths remain available. Keep populated values outside version control. The deployment must supply an authorized OAuth application compatible with the provider, scopes and callback; source fixtures do not establish live acceptance.
