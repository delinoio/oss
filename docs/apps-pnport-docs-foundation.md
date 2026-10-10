# pnport public documentation

## Scope
`apps/public-docs/docs/pnport` owns English guides at https://oss.delino.io/pnport, using the existing consolidated documentation app and publisher.

The content and navigation are implemented at `/pnport` before the first release. The guides describe verified stable 0.1.2 availability through npm latest, signed GitHub archives, the POSIX installer and Homebrew. The 2026-10-04 owner amendment authorizes exactly 0.1.0 preparation with unresolved macOS initialization/cancellation failures and incomplete full acceptance; published stable availability must retain these user-facing limits. The separate 2026-10-05 repair amendment in `docs/project-pnport.md` authorizes exactly 0.1.2 after its retained final-tag and fresh publication gates, with those earlier limits preserved. Stable publication does not change the immutable preview or establish complete acceptance. An owner-authorized experimental `0.1.0-next.N` npm channel may be documented separately, with a published registry check, known feature/minimum-OS/initialization limits, and no Homebrew availability. Prepared sources do not establish published availability. The verified published `0.1.0-next.1` npm next preview and matching signed GitHub prerelease archives are available separately from stable 0.1.2. Guide npm users through a global installation to avoid a conflicting physical project node_modules directory, and Yarn users through a project-local PnP installation and `yarn pnport` invocation.
The published preview's macOS detached-session/group cleanup gap must be explicit: descendants can remain after pnport stops, so external preview tests use foreground commands with daemonization disabled. A later source-only containment fix cannot be described as available in the immutable published preview or as complete stable detached-process acceptance.

## Runtime and Language
Rspress, shared accessible navigation and Cloudflare Pages. Use pnpm dev:public-docs on fixed loopback port 46302. No standalone app, publisher or port.

## Users and Operators
Developers, CI users and editor users; support through GitHub issues with no response SLA.

## Interfaces and Contracts
Hidden tool-cache coexistence and recoverable macOS pre-launch child rejection are available in stable 0.1.2. Preserve earlier 0.1.0/0.1.0-next.1 conflict behavior and never imply those immutable packages gained later capabilities. Explain that a rejected image never runs or gets replaced, callers may handle `ENOTSUP`, and root/privileged/injection failure boundaries remain intact.

Cover installation, commands, supported filesystem behavior, limitations, editor configuration, cache management, diagnostics, reproducible benchmarks and explicit version rollback. Show exact published 0.1.2 npm/Yarn, hosted POSIX installer and Homebrew commands with known limits and a published-version check. Keep Windows examples explicitly future-only until verified 0.2.0 availability. Explain unsupported protected executables, static Linux requirements, graph-change restart behavior, read-only dependencies and ordinary source writes. Do not claim arbitrary-tool or version-specific certification. CLI help/errors/README are English and consistent with the Rust/npm contracts. Describe macOS 15+ as the owner-approved stable minimum; earlier macOS versions are unsupported by 0.1.x. Explain four-target 0.1.x availability and planned Windows x64/arm64 support in 0.2.0 truthfully. Keep the explicitly authorized experimental npm next preview separate, with no undocumented installation target.

The stable clean routes are `/pnport/`, `/pnport/installation`, `/pnport/getting-started`, `/pnport/preview-testing`, `/pnport/commands`, `/pnport/filesystem-and-processes`, `/pnport/editors`, `/pnport/cache`, `/pnport/diagnostics`, `/pnport/benchmarks`, and `/pnport/releases`. The shared selector and Rspress sidebar expose the complete guide set. Installation guidance must never imply that a source-only development build is a published release.

The preview-testing guide owns the external Turbopack development/build/start and TypeScript 7 workflows. Pin the published `@delino/pnport@0.1.0-next.1` version with a successful registry check, keep Yarn's cache inside Turbopack's configured filesystem root, explain that configuration changes belong in a test branch or copy, and run direct dependency binaries from the active workspace. The TypeScript 7 example uses the validated `typescript@7.1.0-dev.20260812.1` nightly and its `tsc` command, with the official TypeScript installation guide as its public reference. Explain that `typescript@7.0.2` lacks the required macOS signing entitlements and is rejected. Preserve preview acceptance limits, doctor readiness versus executable compatibility, deliberate type-error verification, and sanitized result reporting.

Benchmark guidance distinguishes released performance from development observations. Cold/warm comparisons retain identical child work and incremental/application cache state, use at least five samples with median/range, identify instrumentation overhead and filesystem count level, and disclose sampled-memory resolution/shared-page limits. Keep internal harness paths and validation records out of the public guide; publication of release numbers remains gated by full four-target acceptance.

### Application integration

- The 2026-10-05 repair amendment in `project-pnport.md` authorizes exactly stable 0.1.2 after every retained final-tag and fresh publication gate. It retains the previously disclosed initialization/cancellation and full-acceptance limits; earlier 0.1.0 authority does not authorize later versions.

- Describe hidden tool-cache coexistence and recoverable macOS pre-launch child rejection as available in verified stable 0.1.2. Published 0.1.0 and 0.1.0-next.1 still reject physical project node_modules, including cache-only directories. Public guidance must distinguish cache storage from installed dependency trees and preserve the published conflict diagnostics.

- pnport stable 0.1.x targets macOS 15+ on Intel and Apple Silicon, with the same full acceptance standard on both architectures. Keep public installation/support guidance aligned with the owner-approved minimum and retain the separate experimental preview limitations.

- Public pnport guides belong to `apps/public-docs/docs/pnport` at https://oss.delino.io/pnport. Follow `apps-pnport-docs-foundation.md`; use the shared accessible navigation and consolidated fixed-port development/publishing pipeline. Describe published stable 0.1.2 with its unresolved macOS initialization/cancellation failures and incomplete full acceptance. Describe experimental npm next separately, with a registry check before installation and known feature/minimum-OS/initialization limits; never claim availability from preparation alone. Preview npm instructions use global installation to avoid a conflicting physical node_modules directory in the selected PnP project; Yarn instructions use project-local PnP installation and `yarn pnport` invocation. Describe Windows x64/arm64 as planned for 0.2.0, preserve its deferred requirements, and never present its rejecting installer as currently supported. Keep compatibility/release evidence truthful and internal details in docs/.

- Public pnport benchmark guidance must keep development observations distinct from released performance, compare identical child work and incremental/application state across cold/warm caches, and disclose instrumented-count and sampled-memory limits without exposing internal harness or validation paths.

- Public pnport installation guidance must explain package-age quarantine while preserving the user's configured gate. Temporary validation may preapprove only the exact five verified candidate package/version descriptors; record that exception rather than claiming immediate default Yarn acceptance.

- Disclose the published pnport `0.1.0-next.1` macOS detached-session/group cleanup gap in user terms, with foreground/daemonization-disabled preview guidance. Do not attribute a later source-only containment fix to that immutable package or equate containment with complete stable process acceptance.

- Keep `/pnport/preview-testing` in the public sidebar and explicit route catalog, linked from overview, installation and getting started. Its Turbopack and TypeScript 7 walkthrough pins the published pnport preview and validated TypeScript 7.1 nightly, uses the `typescript` package and `tsc` command, preserves the TypeScript 7.0.2 macOS signing limitation, and explains project-local cache placement and sanitized reporting without claiming complete tool acceptance.

## Storage
Preview installation guidance must explain Yarn's package-age quarantine without disabling a user's configured gate. A global npm launcher can run against an already installed PnP project. Temporary release validation may preapprove only the exact five candidate package/version descriptors after checking their public immutable bytes; disclose that exception separately from default-age-gate acceptance.

Static Markdown and shared site assets only. No application storage or remote uploads. Generated output stays ignored.

## Security
Public guides describe user-facing contracts only. Internal architecture, repo-local paths, provenance maintenance and publication operations stay in docs/. Do not publish speculative performance numbers or unverified compatibility claims.

## Logging
Use the existing documentation build/validation logs. Document native diagnostic privacy and stderr/stdout separation without collecting user diagnostics.

## Build and Test
Run pnpm test from apps/public-docs. Validate the eleven routes, required content, internal-content boundaries, selector accessibility, published stable availability, preserved known limits and the production build. Remove generated dist output after verification. Documentation deployment uses only the existing consolidated publisher.

## Dependencies and Integrations
Shared docs-site-switcher and existing Rspress build. Curate from the complete contract and its staged-release amendment; link to stable public interfaces and GitHub support.

## Change Triggers
Update pnport CLI/npm guides, project index, shared navigation/public-site contracts and relevant AGENTS files together.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References
- [Project](project-pnport.md)
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
