# DeliDev parallel browser QA

## Scope

`apps/delidev/scripts/qa/` owns the local QA launcher, bounded HTTP host and
disposable-environment cleanup. `apps/delidev/src/qa/` owns a separate browser
entry using the existing product App. Neither entry is a shipped browser product.
The desktop host, product RPCs, schema allocations and migrations remain owned
by their existing contracts.

## Runtime and Language

Use the repository Node.js/pnpm runtime, JavaScript tooling, Rsbuild and the
existing React/TypeScript frontend. Compile the ordinary Go `delidev` executable
once per run. No CEF build, virtualization or extra product dependency is needed.

## Users and Operators

Maintainers and explicitly authorized MainQA coordinators use this development
tool. Starting it does not authorize a QA agent to use a hosted account, alter an
external repository or issue a production mutation.

## Interfaces and Contracts

Run from the repository root:

```sh
pnpm --filter delidev-desktop dev:qa -- --workers 4
```

The default is four environments; accept integers 1–8 only. Build the QA entry
once without a watcher. Give every environment its own loopback frontend origin,
Go server listener, server UUID, paired client, paired Worker and private state.
Use existing `server run --listen 127.0.0.1:0`, exact `--allowed-origins`,
`device pair-local`, `worker pair-local` and foreground `worker start` commands.
Readiness requires authenticated server/version checks and a connected original
Worker, separately from installed harness or account readiness.

The local `/__qa/` routes expose bootstrap, server status/explicit Start and original
Worker registration/lifecycle/proof/network controls. The separate browser entry
injects an environment-local appearance adapter. These are internal QA interfaces,
not Connect procedures.
They accept closed actions and never executable, scope, endpoint or argv input.
Worker Stop retains its exact original generation. Concurrent mutations are
rejected while the original operation runs; reads never start or repair a child.
Same-identity reconnection retains the existing App and pending product intents.

The browser calls real product Connect RPCs directly. Use the ordinary App,
generated Connect Query services, styles and connection-scoped caches. Do not
fake product responses or native authority. File selection uses the existing
manual path alternative. Trusted-window OAuth callbacks, tray, OS notifications,
CEF child views, saved native windows and desktop installation remain unavailable
and require separate native acceptance. Appearance QA uses isolated local state,
not the native app's saved appearance.

The launcher prints a sanitized run manifest containing worker IDs/URLs, original
source revision, dirty-source indicator, state and evidence location. MainQA may
assign each ready URL to one worker; its model, capacity, recording and coverage
rules remain unchanged. An unavailable native operation is blocked coverage,
never a passing browser check. The external MainQA skill is not edited here.

After the `qa-environments` record lists `ready` environments, give MainQA the
command, its JSON record and the requested coverage. For example:

```text
Use main-qa against the DeliDev browser QA environments started with:
pnpm --filter delidev-desktop dev:qa -- --workers 4
Assign each ready environment URL from the attached qa-environments JSON to
one QA worker. Record screenshots under its artifacts/screenshots directory.
Use the existing repository path input with an explicitly selected test checkout.
Treat the listed native limits as blocked coverage and report them separately.
```

Keep the launcher running until QA workers finish. Each worker uses its own URL,
including in tabs of one Chrome profile. Separate ports isolate origin storage;
the appearance key additionally contains the original server ID. A stopped server
keeps the same endpoint and identity on explicit Start. Starting a server does
not replace a stopped Worker, pair a revoked client, or certify harness readiness.
Server controls remain in the QA banner across Settings navigation. Reloading a
stopped environment retains an explicit Start action, while product screens wait
for renderer-side authenticated identity/version verification. Once verified,
same-identity disconnections retain the existing App and pending intents.

## Storage

Place generated binaries, frontend assets and every server/client/Worker scope
in owner-private temporary directories outside the checkout. New environments
start without user configuration or AI credentials. Keep screenshots and run
manifests separate from deletable environment state. Never track generated `dist`
or validation evidence in the repository.

On shutdown, close browser control admission, drain in-flight operations, and
request existing product session/protected-resource cleanup while the original
Worker remains available. Retain exact request identities while observing work.
Stop and reap only this run's children. Remove an environment only after both
product cleanup and original process exit are confirmed. Unavailable server
authority, unsettled jobs/native ownership, protected references, malformed
metadata or failed process cleanup preserve that environment and report a stable
reason and its recovery location. Killing a process is not native cleanup proof.

Use Ctrl+C or SIGTERM for normal shutdown. The final `qa-cleanup` record and
`artifacts/run.json` report `deleted` or `preserved` per environment. A preserved
record includes its original state path, server identity, endpoint, frontend origin
and stable reasons; the shared binary remains in the parent run directory. Keep
these private scopes intact. Use that binary with the retained `--data-dir` and
`--worker-dir` for product inspection/recovery, never a personal scope. The runner
may start a stopped owned server at its original endpoint for cleanup after browser
admission is closed; it does not silently start a stopped Worker. A protected Worker
network recipient without a product removal operation remains preserved, even
when ordinary metadata work finished. An externally installed or replaced Worker
also retains ownership for separate reconciliation. Do not delete such directories
on the basis of process exit. Artifacts remain separate and are not removed.

Metadata/Git inspection needs no AI account. Actual AI execution requires an
explicitly configured test account and an installed supported harness. The tool
does not copy personal DeliDev configuration or account material.

## Security

Listen only on numeric IPv4 loopback. Each Go server allows only its own QA origin.
The QA host validates Host and Origin, rejects cross-origin control, and uses
no-store responses. Bootstrap exposes only the separately revocable paired
client credential, never owner authority. Keep client/Worker proof credentials
in request-local memory, outside URL parameters, browser storage, query caches,
build output, run manifests and logs. No arbitrary path or command HTTP interface
exists. Owned metadata inspection cannot enumerate the user's native store.
Native entries keep their existing server/reference ownership and cleanup rules.

## Logging

Emit structured operation, worker, phase, state and stable error-code fields.
Do not forward child output, raw exceptions, authentication, native content or
user input. Print generated worker URLs and retained artifact locations only in
the explicit run manifest. Record actual validation revision, commands, results
and remaining browser/native/account limits in PRs, issues and CI artifacts.

## Build and Test

`pnpm test` in `apps/delidev` includes QA host/launcher tests. Integration tests
use real temporary Go servers, Workers, SQLite and Git repositories. Browser
acceptance uses host-supplied Playwright/Chrome, with no product dependency.
Check two same-profile browser pages for independent create/edit/delete/reload,
authentication revocation, Worker inspection/control, server Stop/explicit Start,
cross-origin rejection and package exclusion. Cover startup failure, repeated
controls, signals and cleanup uncertainty. Do not equate these checks with CEF,
real AI account or platform installation acceptance.

Run browser acceptance explicitly:

```sh
DELIDEV_QA_PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs \
  pnpm --filter delidev-desktop test:qa:browser
```

The optional `DELIDEV_QA_BROWSER_CHANNEL` selects an installed browser; default
is Chrome. Both browser pages share one browser context and use actual product
RPCs and Workers. `browser-validation.json` and synthetic screenshots remain in
the external artifacts directory. `test:qa` runs the Go/host/lifecycle integration
tests; ordinary `pnpm test` also verifies that the built release frontend excludes
the QA entry and host markers. No validation record is added to this repository.

## Dependencies and Integrations

Reuse the ordinary CLI, Go server/Worker, protected-storage ownership, generated
TypeScript client, existing App and bounded process-tree termination utilities.
QA builds are separate from ordinary desktop/frontend packaging.

## Change Triggers

Update this contract, the desktop contract, project index/catalog and scoped
app/frontend/script AGENTS when QA ownership or authority changes. Changes to
product lifecycle/credential rules also require their original domain owners.

## References

- [Project index](project-delidev.md)
- [Desktop client](apps-delidev-desktop-contract.md)
- [Go CLI/server/Worker](cmds-delidev-contract.md)
- [Protected credentials](cmds-delidev-credentials-contract.md)
- [Source ownership and validation](cmds-delidev-structure-contract.md)
- [Repository defaults](repository-defaults.md)

The issue #1699 session-remediation layout fixture exercises the real shared task presentation with synthetic closed startup evidence and original-controller counters. Validate bilingual themes, narrow widths and original draft/confirmation retention; keep its separate entry and browser outputs outside product releases. It cannot prove native startup, credential access, real-account acceptance or packaged CEF behavior.

The issue #1699 account-remediation fixture similarly exercises safe ChatGPT/API account failure presentation, original read rechecks and compact rail failure popovers in localized responsive themes. Its separate marker is excluded from release output; synthetic counters never establish real login, OS authorization or account cleanup.
