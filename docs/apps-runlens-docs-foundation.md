# Runlens documentation foundation

## Scope
`apps/runlens-docs` owns the English public Runlens documentation.

## Runtime and Language
Rspress, English Markdown and TypeScript, Cloudflare Pages; ignored doc_build output.

## Users and Operators
CLI users and maintainers installing, configuring, diagnosing, verifying, sharing reports, and manually updating or rolling back Runlens.

## Interfaces and Contracts
Canonical URL: https://runlens.delino.io. Stable clean routes: /, /installation, /configuration, /commands, /verification, /reports, /privacy, /platforms, /troubleshooting, /benchmarks, /releases. All routes appear in navigation and sidebar. Visible repository links appear in social navigation and footer.

Development uses 127.0.0.1:46310; preview uses 127.0.0.1:46272. The shared fixed-port wrapper rejects overrides, fails on conflicts, and forwards signals. Root pnpm dev:runlens-docs delegates to the app.

Document all nine capabilities, direct and configured execution, declarations, preparation, explicit environment names, output separation, schema compatibility, privacy/redaction limits, platform prerequisites, evidence limitations, doctor, benchmark procedures, checksum/Sigstore verification, explicit version installation, and manual update/rollback. Installer routes are /install.sh and /install.ps1. Public discovery links belong in public-docs; internal architecture and operational paths remain in docs.

## Storage
No user data. Build output: apps/runlens-docs/doc_build, ignored and generated explicitly.

## Security
No credentials, private repository details, captured source/streams, or unsubstantiated release/support claims. Hosting configuration alone never publishes or claims a site is live. HTML reports are offline and accessible, with no external resources.

## Logging
Build diagnostics identify route and failure classification without echoing sensitive rejected material.

## Build and Test
Package-local pnpm test builds and validates routes, clean links, navigation, public content, and accessible landmarks. Validate keyboard navigation, fixed ports, installer copies, and public-docs discovery. CI uses the shared affected-workspace planner, a frozen ignore-scripts install, and read-only permissions. Cloudflare Pages builds pnpm --filter runlens-docs build and serves apps/runlens-docs/doc_build.

## Dependencies and Integrations
Use the repository's Rspress version, shared fixed-port wrapper, pnpm apps glob, and Turbo package tasks. No DevHud environment injection or Sites hosting.

## Change Triggers
Update project-runlens, the runtime contract, apps/AGENTS.md, public discovery, and tests together when public behavior or routes change.

## References
- [Project](project-runlens.md)
- [Runtime](crates-runlens-foundation.md)
- [Repository defaults](repository-defaults.md)


## Build validation and installation entrypoints

The app copies the owning `scripts/install/runlens.sh` and `runlens.ps1` into
ignored public `install.sh` and `install.ps1` inputs immediately before Rspress
build. These generated copies must never be independently edited. `pnpm test`
builds the app, runs validator regression tests, and checks every stable clean
route's artifact, English semantic article, complete navigation, GitHub discovery,
public content boundary, installer authentication, and absence of source maps.
Cloudflare Pages serves `doc_build` using clean paths; build output uses flat HTML
files and internal links never use their `.html` suffixes.

`.github/workflows/runlens-docs.yml` provides a manual, default-dry-run deployment
pipeline. Validation produces a private short-lived artifact without credentials.
Only the explicitly selected deploy job enters `runlens-docs-production` and
receives the Cloudflare token/account configuration. The Pages project is
`runlens-docs`; operators configure its custom domain as `runlens.delino.io`.
No site is deployed as part of implementing or validating the PR.
