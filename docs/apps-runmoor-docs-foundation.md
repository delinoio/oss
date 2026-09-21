# apps-runmoor-docs-foundation

## Scope
- Project/component: Runmoor public documentation section
- Canonical path: `apps/public-docs/docs/runmoor`

## Runtime and Language
- Runtime: Runmoor section of the shared Rspress public documentation app.
- Primary language: English Markdown and TypeScript configuration.
- Deployment target: the `public-docs` Cloudflare Pages project.

## Users and Operators
- Runmoor users reading installation, configuration, runner execution, and operations guidance.
- Maintainers reviewing and publishing the documentation.

## Interfaces and Contracts
- Package name: none; the guides are built by `public-docs`.
- Canonical production URL: `https://oss.delino.io/runmoor`.
- Stable clean routes: `/runmoor`, `/runmoor/install`, `/runmoor/configuration`, `/runmoor/commands`, `/runmoor/docker`, `/runmoor/tart`, and `/runmoor/operations`.
- The overview and six guides retain their verification limitations, fork policies, shared-kernel boundaries, and external software licensing guidance after moving into `apps/public-docs/docs/runmoor`. Internal links use the `/runmoor` prefix.
- The retired `https://runmoor.delino.io` host must permanently redirect each suffix to the matching `https://oss.delino.io/runmoor` route; it must not serve duplicate content.
- Use the shared Rspress theme with every Runmoor route in the sidebar. The product switcher is the header's product navigation; the legacy horizontal top-navigation list is intentionally disabled. Keep visible GitHub repository links in the social navigation and document footer.
- Validate the intentional absence of the horizontal top-navigation list, then validate sidebar, social-link, and document-footer regions separately on every Runmoor route. Removing a link from one region must fail even when article content or another region still links to that destination.
- Public content is curated from the Runmoor project and command contracts. User-owned configuration and guest runner paths are public interfaces; repository-internal architecture and operational details remain in `docs/`.
- The CLI release README remains in `cmds/runmoor/README.md` with a link to the integrated public documentation.

## Storage
- Markdown sources live in `apps/public-docs/docs/runmoor`.
- Static output is generated into the ignored `apps/public-docs/doc_build` directory.
- No user data or credentials are stored by this documentation app.

## Security
- Preserve the stable-release verification limits, credential-reference guidance, signature verification steps, trusted-workload restrictions, and third-party licensing boundaries.
- Do not publish internal credentials, private repository paths, or unsupported release claims.
- Preserve the release-availability classifier previously applied by public-docs, including its negation fixtures. Rendered affirmative beta-channel, partial/staged GA, phased/fractional rollout, early-access, and early-announcement claims fail validation; stable-channel disclosures and explicit unavailable/unsupported/prohibited statements remain valid.
- The app-local public-content validator preserves the former public-docs credential/path safeguards. It rejects credential patterns and private filesystem/repository paths in rendered text and HTML comments, plus credential-bearing or forbidden HTML/CSS resource URLs after entity and URL decoding. Credential parameters, including authorization codes, are checked in ordinary queries, direct fragments, and the query portion of hash-routed fragments. Public route exceptions match complete route IDs (with optional query or fragment), never arbitrary paths beginning with a route name. Same-origin static assets remain permitted, as do documented environment/file credential placeholders and XDG paths. Rejections report the page and classification without echoing the rejected value.
- Hosting credentials are managed outside the repository. Adding the app does not create a Cloudflare project, configure DNS, or publish the site.
- Recursively inspect emitted CSS assets as well as HTML. Stylesheet resource URLs and imports use the same credential, internal-path, and clean-URL checks, resolved relative to the stylesheet's public location. Valid generated fonts, static assets, and external stylesheets remain permitted; diagnostics never echo rejected CSS or URL values.

## Logging
- Build and validation logs identify the documentation app and failing route or output file.
- Malformed links fail with a page-level classification; URL parser exceptions and their original input must never be emitted because malformed destinations may contain credentials.
- Development port conflicts include recovery instructions. Logs contain no credentials.

## Build and Test
- Development: `pnpm dev:public-docs` or package-local `pnpm --filter public-docs dev` on fixed port `46302`.
- Build: `pnpm --filter public-docs build`.
- Preview: `pnpm --filter public-docs preview`.
- Validation: `pnpm --filter public-docs test`, building the shared site and validating Runmoor route artifacts, required article headings and links, main landmarks, clean internal links, and public-content restrictions. The command also runs regression fixtures against temporary copies of the real generated HTML to prove rejection of credentials and internal paths without logging their values.
- Clean URL validation covers `href`, `src`, `srcset`, `poster`, `action`, `formaction`, and `data` attributes plus CSS resource references. It resolves entity-encoded, relative, and absolute destinations before checking same-origin `.html` routes; external `.html` URLs and generated static assets remain usable.
- CI: Runmoor changes select `node-public-docs-test` through the shared job-level change plan. The job uses one frozen install with `--ignore-scripts`, the planner's exact Turbo comparison, and successful-main-only cache saves, and participates in `ci-result`.

## Dependencies and Integrations
- Uses the repository's existing Rspress dependency version, the shared `@delinoio/docs-site-switcher` package, and pnpm workspace.
- Cloudflare Pages builds from the repository root with `pnpm --filter public-docs build` and publishes `apps/public-docs/doc_build`.
- `public-docs` owns the Runmoor guide content and discovery links without a second generated copy.

## Change Triggers
- Update this contract, the Runmoor project index, relevant `AGENTS.md` files, and public-docs README when routes, ownership, commands, validation, or hosting change.
- Keep public-docs navigation, Runmoor route checks, and contracts synchronized with this migration.
- Update the documentation catalog when adding or renaming this contract.

## References
- `docs/project-runmoor.md`
- `docs/cmds-runmoor-foundation.md`
- `docs/apps-public-docs-foundation.md`
- `docs/repository-defaults.md`
- `docs/repository-environment-contract.md`

## Native package guidance

The public installation surface documents the exact repository key fingerprint, supported Linux distribution/architecture matrix, stable or explicit preview registration, and package-manager install/update/remove commands. Operational details remain in `docs/repository-linux-packages-contract.md`. Installation guidance must preserve the existing release-archive and other supported installation methods.
