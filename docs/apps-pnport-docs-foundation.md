# pnport public documentation

## Scope
`apps/public-docs/docs/pnport` owns English guides at https://oss.delino.io/pnport, using the existing consolidated documentation app and publisher.

## Runtime and Language
Rspress, shared accessible navigation and Cloudflare Pages. Use pnpm dev:public-docs on fixed loopback port 46302. No standalone app, publisher or port.

## Users and Operators
Developers, CI users and editor users; support through GitHub issues with no response SLA.

## Interfaces and Contracts
Cover installation, commands, supported filesystem behavior, limitations, editor configuration, cache management, diagnostics, reproducible benchmarks and explicit version rollback. Explain unsupported protected executables, static Linux requirements, graph-change restart behavior, read-only dependencies and ordinary source writes. Do not claim arbitrary-tool or version-specific certification. CLI help/errors/README are English and consistent with the Rust/npm contracts. Explain six-target availability truthfully; no preview release or undocumented installation target.

## Storage
Static Markdown and shared site assets only. No application storage or remote uploads. Generated output stays ignored.

## Security
Public guides describe user-facing contracts only. Internal architecture, repo-local paths, provenance maintenance and publication operations stay in docs/. Do not publish speculative performance numbers or unverified compatibility claims.

## Logging
Use the existing documentation build/validation logs. Document native diagnostic privacy and stderr/stdout separation without collecting user diagnostics.

## Build and Test
Run pnpm test from apps/public-docs. Validate routes, required content, internal-content boundaries, selector accessibility and the production build. Remove generated dist output after verification. Documentation deployment uses only the existing consolidated publisher.

## Dependencies and Integrations
Shared docs-site-switcher and existing Rspress build. Curate from the complete #958 contract; link to stable public interfaces and GitHub support.

## Change Triggers
Update pnport CLI/npm guides, project index, shared navigation/public-site contracts and relevant AGENTS files together.

## References
- [Project](project-pnport.md)
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
