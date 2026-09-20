# async-commit-hook public documentation contract

## Scope
`apps/async-commit-hook-docs` owns the documentation-only https://ach.delino.io site, including public installer URLs. The project remains `async-commit-hook`; the results UI belongs to the separate local app.

## Runtime and Language
Rspress 2 with the existing repository dependency range, default theme, search and clean URLs. Sources are English Markdown; Cloudflare Pages serves generated static output.

## Users and Operators
Developers and coding agents reading installation, configuration, exact-commit validation, CLI/MCP, privacy, platform limits and recovery guidance.

## Interfaces and Contracts
Stable routes: `/`, `/install`, `/start`, `/configuration`, `/validation`, `/commands`, `/agents`, `/web`, `/privacy`, `/recovery`, `/compatibility`, `/symlinks`, `/existing-hooks`. All appear in navigation/sidebar, with visible repository links. Every original guide section is migrated without dropping examples or limits; only superseded hosted-UI/pairing behavior changes.

`/docs` redirects to `/docs/`, whose static compatibility page maps known section fragments to their exact new routes and unknown fragments to `/`. It never interprets old pairing, port or run fields. `/install.sh` and `/install.ps1` retain their URLs; their canonical sources are app public files copied unchanged by both Rspress and the release builder.

Development binds 127.0.0.1:46310 and preview 127.0.0.1:46281 using the shared fixed-port wrapper; host overrides and port conflicts fail. Root `pnpm dev:async-commit-hook-docs` delegates through Turbo without team configuration.

## Storage
Markdown lives in `docs`, public assets in `public`, and output in ignored `doc_build`. The site owns no user data, browser credentials or persistent results.

## Security
No local API connections or browser authorization. Rendered public pages must omit credentials, local developer paths and repository-internal architecture. Preserve user-facing configuration placeholders, checksum/Sigstore verification and truthful release/machine-validation limitations. Hosting credentials remain external. Legacy redirects use only an explicit public-route allowlist.

## Logging
Build/tests identify failed routes and classifications without logging secret content. Development conflicts give recovery guidance.

## Build and Test
Package-local `pnpm test` builds and validates route output, main headings, navigation/search/repository links, clean links, public-content restrictions, complete guide subjects, old section redirects, unchanged installers and absence of local RPC client code. Fixed development and preview conflicts fail in integration fixtures. Include the new app and shared wrapper in ach CI selection and package Turbo inputs.

## Dependencies and Integrations
Uses workspace Rspress and the default theme. The existing manual ach release workflow deploys `apps/async-commit-hook-docs/doc_build` to the existing Pages project after its release/Homebrew dependencies. Neither ordinary CI nor dry runs publish. Local UI assets are built and embedded separately.

## Change Triggers
Update this contract, project index, docs catalog, relevant AGENTS, public guidance and release/CI references whenever routes, ports, ownership, installers or hosting change.

## References
- [Project](project-async-commit-hook.md)
- [Local UI](apps-async-commit-hook-contract.md)
- [Release](cmds-async-commit-hook-release-contract.md)
