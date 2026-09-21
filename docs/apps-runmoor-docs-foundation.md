# apps-runmoor-docs-foundation

## Scope
- Project/component: Runmoor public documentation app
- Canonical path: `apps/runmoor-docs`

## Runtime and Language
- Runtime: Rspress static documentation app, aligned with the other repository documentation apps.
- Primary language: English Markdown and TypeScript configuration.
- Deployment target: package-local static output assembled into the `public-docs` Cloudflare Pages publication.

## Users and Operators
- Runmoor users reading installation, configuration, runner execution, and operations guidance.
- Maintainers reviewing and publishing the documentation.

## Interfaces and Contracts
- Package name: `runmoor-docs`, registered by the existing `apps/*` pnpm workspace glob.
- Canonical production URL: `https://oss.delino.io/runmoor`.
- Stable clean routes: `/`, `/install`, `/configuration`, `/commands`, `/docker`, `/tart`, and `/operations`.
- The overview and six guides are owned by this app and assembled under `/runmoor` without removing their content, verification limitations, fork policies, shared-kernel boundaries, or external software licensing guidance. Package-local links remain relative; assembled links receive the `/runmoor` prefix.
- Every assembled page includes the shared site selector with Runmoor selected via `aria-current`; it must offer the root, Nodeup, binpm, and async-commit-hook subpaths.
- Use the default Rspress theme with every stable route in the navigation and sidebar, plus visible GitHub repository links in the social navigation and footer.
- Validate top-navigation, sidebar, social-link, and document-footer regions separately on every stable page. Removing a link from one region must fail even when article content or another region still links to that destination.
- Development uses `127.0.0.1:46309`; production preview uses `127.0.0.1:46271`. Both use the shared fixed-port wrapper, reject host changes, prevent port overrides, fail on conflicts without remapping, and forward termination signals to the server.
- Public content is curated from the Runmoor project and command contracts. User-owned configuration and guest runner paths are public interfaces; repository-internal architecture and operational details remain in `docs/`.
- The CLI release README remains in `cmds/runmoor/README.md` with a link to the consolidated Runmoor subpath.

## Storage
- Markdown sources live in `apps/runmoor-docs/docs`.
- Static output is generated into the ignored `apps/runmoor-docs/doc_build` directory.
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
- Development: package-local `pnpm dev` or root `pnpm dev:runmoor-docs`, independent of the DevHud team environment.
- Build: `pnpm --filter runmoor-docs build`.
- Preview: `pnpm --filter runmoor-docs preview`.
- Validation: package-local `pnpm test` or `pnpm --filter runmoor-docs test`, building the site and validating route artifacts, required article headings and links, main landmarks, base-prefixed clean internal links, absence of retired standalone-origin links or root-path escapes, and public-content restrictions. The command also runs regression fixtures against temporary copies of the real generated HTML to prove rejection of credentials and internal paths without logging their values.
- Preparation: `prepare:app` is an explicit no-op.
- Clean URL validation covers `href`, `src`, `srcset`, `poster`, `action`, `formaction`, and `data` attributes plus CSS resource references. It resolves entity-encoded, relative, and absolute destinations before checking same-origin `.html` routes; external `.html` URLs and generated static assets remain usable.
- CI: `node-runmoor-docs-test` uses the shared job-level change plan, one frozen install with `--ignore-scripts`, the planner's exact Turbo comparison, and successful-main-only cache saves. App changes select both this job and repository-environment validation; changes to the shared Rspress wrapper force documentation checks even outside the workspace graph. The job participates in `ci-result`.

## Dependencies and Integrations
- Uses the repository's existing Rspress dependency version and pnpm workspace.
- `pnpm --filter runmoor-docs build` produces the package output from the repository root. `public-docs` consumes that output under `doc_build/runmoor`; the package output is not independently published.
- Operators may decommission the former standalone Runmoor Pages project and DNS record after consolidated route verification. No redirect or alias is added.

## Change Triggers
- Update this contract, the Runmoor project index, relevant `AGENTS.md` files, and app README when routes, ownership, ports, commands, validation, or hosting change.
- Keep public-docs navigation, removal checks, and contracts synchronized with this migration.
- Update the documentation catalog when adding or renaming this contract.

## References
- `docs/project-runmoor.md`
- `docs/cmds-runmoor-foundation.md`
- `docs/apps-public-docs-foundation.md`
- `docs/repository-defaults.md`
- `docs/repository-environment-contract.md`

## Native package guidance

The public installation surface documents the exact repository key fingerprint, supported Linux distribution/architecture matrix, stable or explicit preview registration, and package-manager install/update/remove commands. Operational details remain in `docs/repository-linux-packages-contract.md`. Installation guidance must preserve the existing release-archive and other supported installation methods.
