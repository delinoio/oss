# Repository Environment Contract

## Scope and authority

This is the internal source of truth for repository configuration, local development environments, and secret ownership. Project contracts may narrow a service allowlist but may not create a second shared secret-loading mechanism. Public product documentation must describe user-visible setup only; it must not expose the team secret paths, upstream project identifiers, local state layout, or operational recovery internals in this document.

Local development has one bounded selector:

```ts
enum DevHudLocalMode {
  Team = "team",
  Oss = "oss",
}
```

Only `DEVHUD_LOCAL_MODE`, the optional non-secret `CARGO_HOME` and `RUSTUP_HOME` tool locations, and the platform-conditional Linux X11/XWayland `DISPLAY` selector, `XAUTHORITY` authority-file location, and `XDG_RUNTIME_DIR` per-user runtime location cross the root Turbo boundary, through the exact task `env` allowlist. Secret names and values must not appear in Turbo `globalEnv`, `globalPassThroughEnv`, task `env`, broad passthrough configuration, summaries, cache metadata, or root process arguments.

## Configuration classes

1. **Committed configuration** is non-sensitive policy required to reproduce the repository: fixed ports and origins, schemas, immutable tool/container versions and digests, safe loopback defaults, validation rules, and `.env.example` files. Examples contain names and placeholders, never usable credentials.
2. **Team development secrets** are the DevHud API and administrator values enumerated below. They are available only to authorized team members through the service-owned Infisical wrappers in `dev`; they are not a CI, production, release, or OSS dependency.
3. **Generated local material** is unique to one checkout and development-only. The OSS identity HMAC key and related state live under ignored `.dev-environment/`, use mode `0600` where supported, are never printed, and are not shared through Infisical. A complete key is published atomically so concurrent commands select the same identity; incomplete or malformed existing material fails closed instead of being hashed. A one-way digest of the validated key scopes the checkout's Compose project and persistent volume without disclosing the key.
4. **User-owned credentials** belong to a developer or product user and remain in their owning local mechanism. This includes binpm registry tokens, DevHud GitHub PATs, DevHud BYO R2 credentials, and contributor-owned Logto users/applications. They must not enter the team development secret manager, synchronized DevHud settings, the API service environment unless explicitly part of the API's official development upload group, or repository files.
5. **Production, deployment, release, and signing secrets** remain in their protected operator/workflow boundaries. Store credentials, platform signing identities, updater signing keys, registry/deployment credentials, production database/Logto/R2/Cloudflare credentials, and release tokens must never be migrated into the team development secret manager.

The `apps/mpapp/.env.example` `EXPO_PUBLIC_*` values are public application configuration and remain package-local; they are not team development secrets. Generic dotenv support and contractually required `.env` Turbo inputs remain supported. Real `.env` files remain ignored.

## Stable commands

- `pnpm env:login` checks for the exact supported Infisical CLI, authenticates interactively only when the current user session is missing, and runs local project initialization only when `.infisical.json` is absent.
- `pnpm env:doctor` performs a non-mutating team tool/authentication/project/path/allowlist check. It emits name/category-only recovery guidance.
- `pnpm dev` selects `team`, fails non-interactively when authentication or local project binding is missing, validates both service environments before migrations, runs the API migration, and starts the DevHud frontend, administrator, and API through `turbo run dev` on fixed ports `46305`, `46306`, and `46307`.
- `pnpm dev:oss` selects `oss`, never invokes Infisical, validates fixed ports, starts repository-owned dependencies, waits for health, runs the API migration, and starts the same three applications through the same Turbo boundary.
- `pnpm dev:oss:down` stops the current checkout's repository-owned dependencies and removes orphan containers without deleting its persistent PostgreSQL volume. Use an explicit Docker volume operation only when intentionally discarding local data.

The tested supported Infisical CLI is exactly [`0.43.116`](https://github.com/Infisical/cli/releases/tag/v0.43.116). Repository commands validate this pin and never install or upgrade it. `.infisical.json` is ignored and must never be committed because it binds an authorized developer's checkout to an upstream project.

Authentication, project, or secret-path failure is terminal for `pnpm dev`. It must not fall back to OSS mode. Resolve the named category with `pnpm env:login` or `pnpm env:doctor`; diagnostics must not print a value, project ID, token, path response, or raw provider error.

## Team service ownership

Infisical injection occurs only inside the owning service wrapper. The root process and Turbo are never wrapped. Both wrappers select environment `dev`, disable personal-secret overriding, expansion, and imports, disable telemetry and verbose output, use argv-based spawning, and buffer provider output until the service allowlist is accepted.

Base child environments preserve only the documented non-secret platform and tool context, including custom `CARGO_HOME` and `RUSTUP_HOME` locations needed by the Rust toolchain. On Linux, the app-owned Native Messaging listener also receives `XDG_RUNTIME_DIR`; it requires that location to be absolute and private before using its per-user socket. The API owns path `/devhud/api`. Its required names are:

- `DEVHUD_DATABASE_URL`
- `DEVHUD_PUBLIC_API_URL`
- `DEVHUD_LOGTO_ISSUER`
- `DEVHUD_LOGTO_AUDIENCE`
- `DEVHUD_LOGTO_DESKTOP_CLIENT_ID`
- `DEVHUD_LOGTO_IOS_CLIENT_ID`
- `DEVHUD_LOGTO_ANDROID_CLIENT_ID`
- `DEVHUD_LOGTO_ADMIN_CLIENT_ID`
- `DEVHUD_ADMIN_REDIRECT_URI`, exactly `http://localhost:46306/auth/callback` in development
- `DEVHUD_PUBLIC_ASSET_BASE_URL`
- `DEVHUD_IDENTITY_HMAC_KEYS`, a comma-separated standard-Base64 key ring with at least 32 decoded bytes per key

The API's optional official-development-upload group is all present or all absent:

- `DEVHUD_R2_ENDPOINT`
- `DEVHUD_R2_ACCESS_KEY_ID`
- `DEVHUD_R2_SECRET_ACCESS_KEY`
- `DEVHUD_R2_STAGING_BUCKET`
- `DEVHUD_R2_PUBLIC_BUCKET`
- `DEVHUD_CLOUDFLARE_API_TOKEN`
- `DEVHUD_CLOUDFLARE_ZONE_ID`
- `DEVHUD_CLOUDFLARE_RATE_LIMIT_RULE_ID`

The administrator owns path `/devhud/admin` and receives only `DEVHUD_LOGTO_ISSUER`. The wrappers reject missing required names, every unknown name, an invalid value, and every partial optional group before migration or service start. Raw HTTP(S) configuration must contain an explicit `scheme://authority`; parser-normalized spellings without an authority delimiter are invalid. HTTP loopback IPv4 authorities must use the canonical four-part decimal notation accepted by Go; shorthand, integer, octal, and hexadecimal spellings are invalid even when WHATWG normalizes them to loopback. They rebuild each child environment from a small platform/tool allowlist and the accepted service values. Values never enter a file, log, diagnostic, argument, or sibling process. Team preflight uses an ephemeral keyed comparison from each wrapper to require the API and administrator issuer values to match exactly before migration without exposing either value. In OSS mode, a contributor who overrides the issuer must record the exact same value in `servers/devhud-api/.env` and `apps/devhud-admin/.env` so API Bootstrap and the administrator CSP remain aligned.

Production-only listener/proxy/updater/sweeper/telemetry settings are not accepted from these team paths. User GitHub PATs and BYO R2 credentials are never accepted. `apps/devhud-admin/.env.example` and `servers/devhud-api/.env.example` document service names and safe local guidance; no root `.env.example` is permitted.

## OSS dependency and capability boundary

`pnpm dev:oss` owns a local Compose definition with these immutable images:

- `postgres:15-bookworm@sha256:5d1d70e254e3c5d7d76847a9deebb18478cd518df37abf6b278d4bdb1fe5d96c`
- `svhd/logto:1.41.0@sha256:7f79547e3d1fe569a3ecae757968a7cfc579687aa8164eec35113c0adc983c5b`

PostgreSQL binds `127.0.0.1:5432`, Logto core binds `127.0.0.1:3001`, and Logto Console binds `127.0.0.1:3002`; the applications bind `46305`, `46306`, and `46307`. The administrator preflight probes the same `localhost` host used by its development server. A conflicting owner is an actionable failure, never a port remap. An already-running dependency is accepted only when the current checkout's identity-derived Compose project owns the exact binding.

OSS mode creates a unique identity HMAC key without printing it. It supplies safe loopback database/API/issuer/asset defaults and deliberately omits the complete R2/Cloudflare group, so Bootstrap does not advertise official uploads. Guest/bootstrap development remains functional. The repository does not fabricate a shared Logto administrator, user, API resource, application, or client secret. Authenticated flows and administrator sign-in remain unavailable until a contributor creates and owns the needed local Logto objects, records their public issuer/audience/client configuration in the service-local files as documented above, or uses the authorized team path.

Termination and child failure stop the active process tree and reap partial startup. Every Infisical, Docker, migration, and Turbo preflight, startup, or cleanup child is lifecycle-tracked while the root signal handlers are installed, including version, authentication, context, and Compose-ownership probes; buffered probes complete only after their stdout and stderr pipes close. OSS cleanup stops only the current checkout's identity-derived Compose project even when dependency startup, migration, or steady-state application execution is interrupted, while preserving that checkout's volumes. An interruption already being handled does not suppress cleanup, and a later signal is forwarded to the active cleanup process tree. Docker availability, ownership, startup, and cleanup children preserve only the contributor's `DOCKER_HOST` and `DOCKER_CONTEXT` daemon selectors in addition to the base process allowlist; those selectors do not enter Turbo or service children. `DOCKER_CONTEXT` retains Docker's precedence over `DOCKER_HOST`, but `pnpm dev:oss` accepts only an effective Unix socket, Windows named pipe, or loopback TCP endpoint and rejects remote or uninspectable daemons before Compose startup because the dependencies publish loopback-only ports for local services.

## Implementation and validation

Root scripts are orchestration-only cross-platform Node ESM. `servers/devhud-api` is a pnpm workspace package and owns its local validation, migration, and serve wrapper; `apps/devhud-admin` owns its development wrapper. Every child is spawned with an explicit argv array and `shell: false`; Windows pnpm `.cmd` shims are invoked explicitly through `cmd.exe` rather than treated as native executables.

No-network tests use fake Infisical, Docker, Go, and Turbo executables, an injected temporary generated-state directory, and an injected Infisical project-config directory outside the checkout; they never delete or replace the checkout's `.dev-environment` identity or `.infisical.json` binding. They cover first/repeat team setup through the explicit login command; non-interactive startup readiness; exact provider flags, paths, and service allowlists; complete provider-pipe marker handling; missing/unknown/partial/malformed-authority configuration and numeric IPv4 alias rejection; canary non-disclosure; fail-closed authentication/path behavior; OSS ordering and lack of Infisical; matching administrator/API issuers in team and OSS modes; local Docker endpoint enforcement; immutable pins; every fixed port and bind host; atomic generated-key publication, validation, permissions, and checkout-scoped Compose identity; custom Rust toolchain locations; platform-conditional Linux session/runtime context; migration-before-Turbo ordering; unavailable OSS authenticated/admin/official-upload capability; preflight, startup, migration, steady-state, and cleanup interruptions with process-tree cleanup; and volume-preserving down. CI installs this suite's workspace dependencies without lifecycle scripts and runs it on Linux, macOS, and Windows.

Changes to this contract require synchronized updates to `docs/README.md`, applicable project/domain contracts and service READMEs, root/domain `AGENTS.md`, package scripts, tests, and the lockfile. Validation also includes Go API/config/migration tests, affected frontend tests, a clean frozen-lockfile install, resolved Turbo configuration inspection, `git diff --check`, and checks that no generated root `.env`, `.infisical.json`, or secret-bearing state is committed.
