# AIPRouter distribution assets

The [operations contract](../../docs/servers-aiprouter-operations-contract.md) owns these assets. Public operator guidance belongs to the documentation application. These commands describe the packaging interface for maintainers.

## Compose

Linux amd64 and arm64 are server targets. Docker Desktop on macOS and Windows is for development/evaluation. Run Compose from this directory. Copy `.env.example` to `.env`, restrict it to mode 600, and replace every placeholder. Generate each encryption key with `openssl rand -base64 32`. Keep database passwords URL-safe. Store master keys and database backups separately.

Set the public HTTPS origin and register its `/auth/callback` URL with the OIDC provider. Provider OAuth callback addresses must use the same public origin and the callback path supplied by that integration. The existing operator reverse proxy terminates TLS; examples are in `proxy/`. Do not expose health or metrics routes publicly. Configure SMTP, R2, and the exact OIDC issuer/subject operator allowlist before production use. Private provider destinations require operator approval; the mock provider is not implicitly approved in production.

```sh
docker compose build
docker compose up -d --wait postgres
docker compose run --rm --no-deps aiprouter migrate
docker compose up -d aiprouter
docker compose --profile observability up -d prometheus grafana
```

The production file publishes only loopback ports and no PostgreSQL port. `compose.fixture.yml` adds synthetic OIDC identities (`alice`, `bob`, `unverified`), an SMTP receiver, and a mock provider. The server shares the fixture network namespace so browser and server agree on the loopback issuer. The override requires Compose 2.24 or later. The SMTP receiver counts accepted messages and discards content. `/fixture/mail` reports the count. Fixture provider models `fixture-429` and `fixture-503` produce reproducible errors; `fixture-model` supports JSON and SSE chat responses. This fixture does not implement R2 or full provider compatibility. The service/adapter tests own those cases.

```sh
docker compose -f compose.yml -f compose.fixture.yml up -d --build
```

Fixtures must remain local. Use `scripts/aiprouter/compose-smoke.py` from the root for an isolated disposable installation/recovery test. It refuses an existing `.env` and removes only its own Compose project. It requires free loopback ports 8080 and 18090 and a working Docker daemon.

The server image builds the console with Node.js 24 and the root-pinned pnpm version using the frozen workspace lockfile. It copies the Rsbuild `dist` output to `/app/console` and sets `AIPROUTER_CONSOLE_DIR` to that path. The server owns static routing and serves the console from the same origin as the API. Node.js and build dependencies remain in the build stage. Dockerfile-specific context filters exclude host dependency directories, generated outputs, and environment files; no host dependency mounts are used. Keep the console manifest and root lockfile synchronized before building the image. The image includes Go and console dependency license notices.

## Maintenance interface

Run `python3 scripts/aiprouter/maintenance.py` from the repository root with:

- `backup /absolute/path/backup.dump`: creates a mode-600 custom PostgreSQL archive, refuses overwrite, and excludes `aiprouter_temp` entirely. Encryption keys are never included.
- `restore /absolute/path/backup.dump`: stops the server, removes old temporary state, restores durable state transactionally, migrates, and invalidates all restored authority before any restart.
- `rotate`: stops the server and invokes verified re-encryption with the configured active key. Configure both old and new keys first. Remove the old active key only after successful completion; retain separate copies required for old backups.
- `upgrade`: stops the server and runs migrations using the selected image. Back up first. Rollback requires the prior image and its matching database backup; apply restore invalidation before restart.

Every mutation leaves the server stopped, including on failure. The operator explicitly runs `docker compose up -d aiprouter` only after success. Stop fixture sidecars before maintenance. Recovery requires fresh login, keys, invitations, and CLI connections. These commands assume the single-host Compose instance is the only process admitting traffic to this database; stop any separately managed instance too. Backup scheduling and retention remain operator responsibilities.

### Recover orphaned admission slots

Admission slots do not expire by time: a lost process or database write does not prove that an upstream execution stopped. Recover only the identified boot instance. Each new server process registers a fresh instance ID before listening; a terminated or previously registered ID cannot be reused. Database migration does not register a boot.

Run the wrapper from the repository root. Status prints safe metadata (`id`, `state`, `created_at`, `active_admissions`) and does not reveal proof values:

```sh
python3 scripts/aiprouter/maintenance.py admission-status
python3 scripts/aiprouter/maintenance.py admission-drain INSTANCE_ID --proof-file /private/path/recovery-proof
```

Drain fences new admissions from the selected boot while already dispatched work may continue. It creates a new owner-only mode-0600 file exclusively; it refuses an existing path. Keep that one-use proof private and out of shell arguments, environment variables, logs, backups, and source control. Draining again creates a new proof and invalidates the previous one. If proof delivery fails, drain again to a new file.

Stop the selected boot process and prevent it from resuming. For the single-host Compose service, run `docker compose --project-directory packaging/aiprouter -f packaging/aiprouter/compose.yml stop aiprouter`. Independently confirm that every upstream execution dispatched by that boot has stopped. Stopping a container or observing an old timestamp does not establish remote upstream termination. If this cannot be confirmed, leave its slots reserved.

Only after both conditions are established, reconcile:

```sh
python3 scripts/aiprouter/maintenance.py admission-reconcile INSTANCE_ID --proof-file /private/path/recovery-proof --confirm-instance-stopped --confirm-upstreams-stopped
```

The wrapper requires both explicit acknowledgments, stops the local Compose service, passes the proof through stdin, and captures subprocess output without logging it. It rejects symlink, shared-access, or foreign-owned proof files. Success consumes the proof and removes its local file; failure preserves the private file for inspection/retry. A repeated or superseded proof is rejected. If the response was lost after server success, use status to confirm termination and remove the now-consumed proof file manually. The service remains stopped until explicitly started with a new boot ID. Only the selected instance's slots are released; unrelated instances, keys, and sessions remain valid. Never use `restore-invalidate` as a slot-recovery shortcut; it belongs only to disaster recovery after restoring a database.

The underlying executable interface is `admission-status`, `admission-drain <instance-id>` (proof plus newline on stdout), and `admission-reconcile <instance-id> --upstreams-stopped` (bounded proof on stdin; JSON `instance_id` and `released_admissions` on success). Use the wrapper to protect proof output and enforce both confirmations. Real upstream-stop verification remains the operator's responsibility; automated fixtures cannot establish it for production.

## Validation and distribution

`scripts/aiprouter/validate.sh` checks mock servers, maintenance ordering/failure handling, installer integrity, and Compose schema without a daemon. CI additionally runs Compose installation/recovery, alert timing fixtures, cross-compilation, package inspection, and deterministic mock load on Linux. None establishes live account, production, or native OS acceptance.

`scripts/aiprouter/build-release.py /new/output/path` builds five CLI targets and two Linux server targets, writes SHA256SUMS, gathers complete Go dependency licenses/notices, and assembles an npm tarball. The npm installer selects the exact platform and verifies its binary against checksums embedded in the tarball. It rejects unsupported platforms or missing/mismatched checksums. Template sources are not publishable by themselves; checksums and licenses are injected only during staging. This command does not publish. Before an actual release, verify npm scope ownership and publishing authority for `@delino/aiprouter`, review dependency advisories, validate package contents, and bind release assets to the staged checksums. Build success does not establish OS credential-store or background-service acceptance.

`scripts/aiprouter/load.py --output /tmp/aiprouter-load.json` sends a fixed synthetic workload to the mock provider. Set `--url` to test the router and provide its synthetic key through `AIPROUTER_LOAD_KEY`; never use a real user's content. Reports include revision, workload, hardware, harness resources, latency, and error counts. Supply measured server resources through `--server-resources` when measured externally. An absent server measurement is explicitly reported. This is not an SLO or a live-provider capacity claim.

`scripts/aiprouter/router-load.py --output /tmp/aiprouter-router-load.json` measures the authenticated router against the mock upstream. Supply `AIPROUTER_TEST_DATABASE_URL` for a disposable PostgreSQL server with database-creation privileges. The harness creates and removes its own database, seeds two users across four organizations, sets explicit fixture limits, and sends the fixed workload through one issued key. It samples router process CPU/RSS and records seed/limit conditions. Credentials remain in memory and child environments. It requires free loopback ports 8080, 18090, and 1025. This is the CI load path; direct fixture load alone does not satisfy router load validation.

The three alerts cover one-minute service failure, one-minute DB failure, and three consecutive cleanup failures or fifteen minutes without cleanup success. Grafana shows aggregate health/outcome series only. Prometheus evaluates alerts; operators supply their own Alertmanager routing and any capacity/error-rate policies. Enable OTLP export through the server-supported environment and an operator-owned collector; do not expose telemetry publicly or add prompt/response attributes.
