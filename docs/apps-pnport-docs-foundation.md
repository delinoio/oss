# pnport public documentation

## Scope
`apps/public-docs/docs/pnport` owns English guides at https://oss.delino.io/pnport, using the existing consolidated documentation app and publisher.

The content and navigation are implemented at `/pnport` before the first release. The guides retain the stable 0.1.0 unreleased notice until its actual distribution is verified. The 2026-10-04 owner amendment authorizes exactly 0.1.0 preparation with unresolved macOS initialization/cancellation failures and incomplete full acceptance; eventual stable availability must retain these user-facing limits. This authorization does not change the immutable preview or establish published stable availability. An owner-authorized experimental `0.1.0-next.N` npm channel may be documented separately, with a published registry check, known feature/minimum-OS/initialization limits, and no Homebrew availability. Prepared sources do not establish published availability. The verified published `0.1.0-next.1` npm next preview and matching signed GitHub prerelease archives are available separately from stable 0.1.0. Guide npm users through a global installation to avoid a conflicting physical project node_modules directory, and Yarn users through a project-local PnP installation and `yarn pnport` invocation.
The published preview's macOS detached-session/group cleanup gap must be explicit: descendants can remain after pnport stops, so external preview tests use foreground commands with daemonization disabled. A later source-only containment fix cannot be described as available in the immutable published preview or as complete stable detached-process acceptance.

## Runtime and Language
Rspress, shared accessible navigation and Cloudflare Pages. Use pnpm dev:public-docs on fixed loopback port 46302. No standalone app, publisher or port.

## Users and Operators
Developers, CI users and editor users; support through GitHub issues with no response SLA.

## Interfaces and Contracts
Cover installation, commands, supported filesystem behavior, limitations, editor configuration, cache management, diagnostics, reproducible benchmarks and explicit version rollback. Show future npm/Yarn, hosted direct-installer, and Homebrew commands only behind an explicit unreleased warning and a published-version check. Explain unsupported protected executables, static Linux requirements, graph-change restart behavior, read-only dependencies and ordinary source writes. Do not claim arbitrary-tool or version-specific certification. CLI help/errors/README are English and consistent with the Rust/npm contracts. Describe macOS 15+ as the owner-approved stable minimum; earlier macOS versions are unsupported by 0.1.0. Explain four-target 0.1.0 availability and planned Windows x64/arm64 support in 0.2.0 truthfully; only the explicitly authorized experimental npm next preview, with no undocumented installation target.

The stable clean routes are `/pnport/`, `/pnport/installation`, `/pnport/getting-started`, `/pnport/preview-testing`, `/pnport/commands`, `/pnport/filesystem-and-processes`, `/pnport/editors`, `/pnport/cache`, `/pnport/diagnostics`, `/pnport/benchmarks`, and `/pnport/releases`. The shared selector and Rspress sidebar expose the complete guide set. Installation guidance must never imply that a source-only development build is a published release.

The preview-testing guide owns the external Turbopack development/build/start and TypeScript 7 workflows. Pin the published `@delino/pnport@0.1.0-next.1` version with a successful registry check, keep Yarn's cache inside Turbopack's configured filesystem root, explain that configuration changes belong in a test branch or copy, and run direct dependency binaries from the active workspace. The TypeScript 7 example uses the validated `typescript@7.1.0-dev.20260812.1` nightly and its `tsc` command, with the official TypeScript installation guide as its public reference. Explain that `typescript@7.0.2` lacks the required macOS signing entitlements and is rejected. Preserve preview acceptance limits, doctor readiness versus executable compatibility, deliberate type-error verification, and sanitized result reporting.

Benchmark guidance distinguishes released performance from development observations. Cold/warm comparisons retain identical child work and incremental/application cache state, use at least five samples with median/range, identify instrumentation overhead and filesystem count level, and disclose sampled-memory resolution/shared-page limits. Keep internal harness paths and validation records out of the public guide; publication of release numbers remains gated by full four-target acceptance.

## Storage
Preview installation guidance must explain Yarn's package-age quarantine without disabling a user's configured gate. A global npm launcher can run against an already installed PnP project. Temporary release validation may preapprove only the exact five candidate package/version descriptors after checking their public immutable bytes; disclose that exception separately from default-age-gate acceptance.

Static Markdown and shared site assets only. No application storage or remote uploads. Generated output stays ignored.

## Security
Public guides describe user-facing contracts only. Internal architecture, repo-local paths, provenance maintenance and publication operations stay in docs/. Do not publish speculative performance numbers or unverified compatibility claims.

## Logging
Use the existing documentation build/validation logs. Document native diagnostic privacy and stderr/stdout separation without collecting user diagnostics.

## Build and Test
Run pnpm test from apps/public-docs. Validate the eleven routes, required content, internal-content boundaries, selector accessibility, explicit unreleased state and the production build. Remove generated dist output after verification. Documentation deployment uses only the existing consolidated publisher.

## Dependencies and Integrations
Shared docs-site-switcher and existing Rspress build. Curate from the complete #958 contract and its staged-release amendment; link to stable public interfaces and GitHub support.

## Change Triggers
Update pnport CLI/npm guides, project index, shared navigation/public-site contracts and relevant AGENTS files together.

## References
- [Project](project-pnport.md)
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
