# async-commit-hook public documentation contract

## Scope
`apps/public-docs/docs/async-commit-hook` owns the public async-commit-hook content and is published at `https://oss.delino.io/async-commit-hook`. The project remains `async-commit-hook`; the results UI belongs to the separate local app. Installer assets are sourced from `scripts/install` and published by the consolidated `public-docs` build.

## Runtime and Language
Rspress 2 with the existing repository dependency range, default theme, search and clean URLs. Sources are English Markdown owned directly by the `public-docs` application; its static output is published through Cloudflare Pages.

## Users and Operators
Developers and coding agents reading installation, configuration, exact-commit validation, CLI/MCP, privacy, platform limits and recovery guidance.

## Interfaces and Contracts
Stable routes: `/async-commit-hook/`, `/async-commit-hook/install`, `/async-commit-hook/start`, `/async-commit-hook/configuration`, `/async-commit-hook/validation`, `/async-commit-hook/commands`, `/async-commit-hook/agents`, `/async-commit-hook/web`, `/async-commit-hook/privacy`, `/async-commit-hook/recovery`, `/async-commit-hook/compatibility`, `/async-commit-hook/symlinks`, `/async-commit-hook/existing-hooks`. All appear in grouped top navigation and the sidebar, with visible repository links. Every original guide section is migrated without dropping examples or limits; only superseded hosted-UI/pairing behavior changes.

`/async-commit-hook/docs` redirects to `/async-commit-hook/docs/`, whose static compatibility page maps known section fragments to their exact new routes and unknown fragments to `/async-commit-hook/`. It never interprets old pairing, port or run fields. The canonical `/async-commit-hook/install.sh` and `/async-commit-hook/install.ps1` entrypoints are copied unchanged from `scripts/install/async-commit-hook.sh` and `scripts/install/async-commit-hook.ps1`.

Development uses the consolidated `public-docs` server on `127.0.0.1:46302`; host overrides and port conflicts follow the public-docs development contract.

Primary public shell and PowerShell install commands omit an explicit product version and use the downloaded installers' latest-published-stable defaults (subject to the documented shell `ACH_VERSION` override and PowerShell `-Version`). The primary upgrade command is `ach self-update`, which selects the highest published stable version and refuses implicit downgrades. Separate explicit-version examples use a clearly replaceable `MAJOR.MINOR.PATCH` placeholder for intentional selection or rollback; automatic Public Docs deployment must remain safe while a release is prepared but unpublished. Production-output tests enforce these command and guidance boundaries.

Release Project synchronizes the local UI, generated client, Go version and release metadata. These four version fields are one atomic preparation boundary; installer defaults resolve published releases independently, and published docs contain no pinned primary install/update commands.

## Storage
Markdown lives in `apps/public-docs/docs/async-commit-hook`, installer assets are maintained in `scripts/install`, and output is generated in the ignored `apps/public-docs/doc_build`. The site owns no user data, browser credentials or persistent results.

## Security
No local API connections or browser authorization. Rendered public pages must omit credentials, local developer paths and repository-internal architecture. Preserve user-facing configuration placeholders, checksum/Sigstore verification and truthful release/machine-validation limitations. Hosting credentials remain external. The `/async-commit-hook/docs` migration route is the only retained compatibility route and uses an explicit public-route allowlist; no retired standalone-host redirects or aliases are added.

## Logging
Build/tests identify failed routes and classifications without logging secret content. Development conflicts give recovery guidance.

## Build and Test
`pnpm --filter public-docs test` builds and validates route output, main headings, navigation/search/repository links, clean links, public-content restrictions, complete guide subjects, the `/async-commit-hook/docs` section migration route, unchanged installers and absence of local RPC client code. Fixed development conflicts follow the consolidated public-docs contract.

## Dependencies and Integrations
Uses workspace Rspress and the default theme through `public-docs`. The async-commit-hook release workflow publishes release assets only; it does not deploy documentation. `public-docs` builds and publishes the `/async-commit-hook` section and is the sole documentation publisher. Neither ordinary CI nor dry runs publish. Local UI assets are built and embedded separately.

## Change Triggers
Update this contract, project index, docs catalog, relevant AGENTS, public guidance and release/CI references whenever routes, ports, ownership, installers or aggregation behavior changes. After consolidated publication is verified, operators decommission the former standalone Pages project and DNS record without adding redirects.

## References
- [Project](project-async-commit-hook.md)
- [Local UI](apps-async-commit-hook-contract.md)
- [Release](cmds-async-commit-hook-release-contract.md)
