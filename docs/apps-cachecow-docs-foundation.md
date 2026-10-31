# CacheCow public documentation

## Scope

Own user guides at `apps/public-docs/docs/cachecow`, rendered by the existing public-docs app. Guides distinguish intended unreleased 0.1.0 workflows from published artifacts and verified native support.

## Runtime and Language

Markdown within the existing Rspress public-docs app, using its Node.js 24 baseline and Cloudflare Pages publisher. No separate app, hosted CacheCow service, or web/mobile product UI.

## Users and Operators

Developers, coding agents, CI operators, and users installing or troubleshooting CacheCow.

## Interfaces and Contracts

The public section is `/cachecow` under `https://oss.delino.io`. Its eight routes are `/cachecow/`, `/cachecow/installation`, `/cachecow/configuration`, `/cachecow/cache`, `/cachecow/concurrency`, `/cachecow/shims`, `/cachecow/output`, and `/cachecow/troubleshooting`. Register them in the shared route catalog and existing sidebar. The root sidebar provides an entry point without adding navbar items. Every guide includes an unreleased 0.1.0 availability notice.

Do not invent frontend integration formats, archive names, JSON field names, shell identifiers, or color opt-out syntax before runtime interfaces are finalized. Describe established behavior and explicitly identify details still awaiting integration.

Guides must cover installation and supported environments; preset configuration and persistent-cache integration; source selection and configuration precedence; prepare/run/status/clean/doctor; concurrency and process lifecycle; failure recovery; copy-mode fallback; rollback; performance limitations; and GitHub issue support.

Include shim install/list/doctor/uninstall, Bash/Zsh/PowerShell 7 session activation and persistent setup/removal, confirmation and `--yes`, `--file`, current-session guidance, PATH priority, real-tool lookup, rustup selection, bypass, pass-through behavior, nesting, and lock-aware Cargo clean. Explain unsupported aliases/functions, absolute paths, package-manager-local binaries/scripts, and JS API interception, with explicit `ccow run` and frontend configuration alternatives.

Document the difference between `ccow clean` rejecting active use and shim-dispatched `cargo clean` waiting without preparation. Explain that concurrent unmanaged cache writers are unsupported. Do not promise cache hits, shared compiler heaps, shared page-cache memory, or fixed performance savings. Publish measured physical storage, preparation time, build time, and memory with environment details and limits when acceptance evidence exists.

## Storage

Sources live in the content root. The shared app owns generated output and publication. No product runtime data or telemetry belongs in the documentation site.

## Security

Curate user-facing workflows and stable public interfaces. Keep internal architecture, repository paths, operational details, credentials, raw native contents, and user state out of public guides.

## Logging

Use the existing public-docs safe build and route-validation logs. CacheCow adds no remote metrics or telemetry service.

## Build and Test

Run `pnpm --filter public-docs test` for changes under the app. Use root `pnpm format` and `pnpm format:check` for changed Markdown. The shared app owns build, route, accessibility, public-content, and link validation. Documentation tests do not establish native tool, filesystem, installation, or performance acceptance. Development uses `pnpm dev:public-docs` on fixed loopback port 46302; conflicts fail.

## Dependencies and Integrations

Consumes the complete requirements, command and distribution contracts. Uses the [public-docs foundation](apps-public-docs-foundation.md) and [project index](project-public-docs.md); no separate deployment.

## Change Triggers

Update the project index, complete requirements, affected consumer contracts, and public guides together when interfaces change. Follow the [instruction-update policy](README.md#instruction-update-policy). Keep new validation records in PRs and CI artifacts.

## References

- [Project index](project-cachecow.md)
- [Complete requirements](cmds-cachecow-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)
