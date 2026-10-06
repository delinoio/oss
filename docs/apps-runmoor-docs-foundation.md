# Runmoor public documentation foundation

## Scope
- Project/component: Runmoor public documentation content
- Canonical path: `apps/public-docs/docs/runmoor`

## Runtime and Language
- Runtime: Rspress static documentation app, aligned with the other repository documentation apps.
- Primary language: English Markdown and TypeScript configuration.
- Deployment target: the `apps/public-docs` static output published by the consolidated Cloudflare Pages site.

## Users and Operators
- Runmoor users reading installation, configuration, runner execution, and operations guidance.
- Maintainers reviewing and publishing the documentation.

## Interfaces and Contracts
- The content is built by the `public-docs` Rspress application; there is no standalone Runmoor documentation package or workspace.
- Canonical production URL: `https://oss.delino.io/runmoor`.
- Stable clean routes: `/runmoor/`, `/runmoor/install`, `/runmoor/configuration`, `/runmoor/commands`, `/runmoor/docker`, `/runmoor/tart`, `/runmoor/host`, and `/runmoor/operations`.
- The overview and seven guides are owned by this content section and published under `/runmoor` without removing their content, verification limitations, fork policies, shared-kernel boundaries, or external software licensing guidance. Links use the consolidated site's `/runmoor` base.
- The operations guide explains that service start, stop, and uninstall use the same configuration path recorded at installation, and provides safe inspection and recovery steps for a configuration mismatch without exposing repository-internal paths.
- The operations guide identifies completed-Stop recovery after an interrupted service reload as unreleased. It explains that one explicit Start resumes a verified inactive service after the reload command and cleanup finish, preserves pauses, and requires inspection of uncertain ownership instead of deleting recovery files.
- Every page includes the shared site selector with Runmoor selected via `aria-current`; it must offer the root, Nodeup, binpm, async-commit-hook, and clibox subpaths.
- Use the default Rspress theme with every stable route in the navigation and sidebar, plus visible GitHub repository links in the social navigation and footer.
- Validate top-navigation, sidebar, social-link, and document-footer regions separately on every stable page. Removing a link from one region must fail even when article content or another region still links to that destination.
- Development uses the consolidated `public-docs` server at `127.0.0.1:46302`; it owns the Runmoor section alongside the other project sections.
- Public content is curated from the Runmoor project and command contracts. User-owned configuration and guest runner paths are public interfaces; repository-internal architecture and operational details remain in `docs/`.
- Tart recovery guidance explains that ambiguous ownership keeps the VM, Runmoor state/data and capacity reservation intact even during force-stop. It directs users to restore a paired backup that contains the VM identity proof or to reimport the source under a new Runmoor identity while preserving uncertain resources; it never suggests repairing markers manually or deleting a same-named VM.
- The Docker guide and CLI README describe volume deletion revalidation as unreleased until a containing release is verified. Explain preservation of conflicting volumes and incomplete cleanup, retry after failed inspection, non-force removal and the remaining replacement window between inspection and deletion. Keep label keys, internal records and API implementation details in the command contract; public guidance must not claim atomic race protection.
- The CLI release README remains in `cmds/runmoor/README.md` with a link to the consolidated Runmoor subpath.

## Storage
- Markdown sources live in `apps/public-docs/docs/runmoor`.
- Static output is generated into the ignored `apps/public-docs/doc_build` directory.
- No user data or credentials are stored by this documentation app.

## Security
- Installation examples select one exact published release tag, derive archive download URLs and the tag-triggered signing identity from it, and verify both Sigstore bundles and the selected archive checksum before extraction. Manual-main signatures require their separately documented exact identity; never use an arbitrary-version identity wildcard. Keep the CLI README in sync.
- Preserve the stable-release verification limits, credential-reference guidance, signature verification steps, trusted-workload restrictions, and third-party licensing boundaries.
- Public configuration guidance identifies `RUNMOOR_PAT` as a GitHub PAT environment reference, explains where to create fine-grained and classic PATs and the repository/organization runner permissions, and distinguishes it from short-lived runner registration tokens. Ubuntu user-service guidance shows a user-owned mode 0600 credential file with an absolute TOML file reference, explains the systemd user-manager environment boundary and the explicit connection refresh/resume after token rotation, and preserves the user-session availability limit. An environment-backed service already running without the imported PAT requires stop/start and pool resume. Never place a PAT value in public examples, TOML, or service definitions.
- Operations guidance documents the fail-closed recovery boundary for an ambiguous legacy SQLite location: stop affected managers, preserve complete backups of both locations, and do not move or delete either database before explicit review.
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
- Development: `pnpm --filter public-docs dev`, independent of the DevHud team environment.
- Build: `pnpm --filter public-docs build`.
- Preview: use the `public-docs` preview command for the consolidated output.
- Validation: `pnpm --filter public-docs test`, building the site and validating Runmoor route artifacts, required article headings and links, main landmarks, base-prefixed clean internal links, absence of retired standalone-origin links or root-path escapes, and public-content restrictions.
- Clean URL validation covers `href`, `src`, `srcset`, `poster`, `action`, `formaction`, and `data` attributes plus CSS resource references. It resolves entity-encoded, relative, and absolute destinations before checking same-origin `.html` routes; external `.html` URLs and generated static assets remain usable.
- CI: `node-public-docs-test` uses the consolidated documentation build and validation boundary, including the Runmoor content section.

## Dependencies and Integrations
- Uses the repository's existing Rspress dependency version through the `public-docs` pnpm workspace.
- `pnpm --filter public-docs build` produces the complete output, including `doc_build/runmoor`; no standalone Runmoor documentation output is published.
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

The installation page and CLI README also document `brew install delinoio/tap/runmoor`, version inspection, explicit Homebrew upgrades and removal for macOS 14+ Apple Silicon. Homebrew uses the same signed public archive as direct installation, and checks the Formula's pinned SHA-256 during installation. Preserve direct archive/Sigstore instructions and clarify that Tart, runner configuration and Runmoor service registration remain operator-owned. Intel and Linux Homebrew are not supported.

Runmoor `0.1.3` native packages have passed the complete public installation matrix and are documented as available through stable. Shared executable installation/update/removal examples use Runmoor; other projects require their own verified publication before availability claims change. Public installation guidance must distinguish package compatibility from manager runtime support: Linux manager execution requires Ubuntu 22.04+ on x86-64 or ARM64, while non-Ubuntu package checks prove installation and version/help execution only.

The public installation surface documents the exact repository key fingerprint, supported Linux distribution/architecture matrix, stable or explicit preview registration, and package-manager install/update/remove commands. Operational details remain in `docs/repository-linux-packages-contract.md`. Installation guidance must preserve the existing release-archive and other supported installation methods.

The Runmoor install page links directly to the shared key-verification and stable-registration sections before showing installation commands. It explains that key inspection alone does not register an APT source, requires a successful package-list refresh and a visible package candidate, and links to shared troubleshooting for missing packages. Installation, upgrade, and removal commands use separate copyable blocks so copying an installation example cannot immediately uninstall the package. The shared registration page owns architecture, source, suite, update-error, and candidate diagnostics.

## Service reload documentation

Describe version-aware service reload as unreleased until a containing release
is verified. After the operator installs a newer CLI, `runmoor reload` checks the
running owned launchd/systemd manager and advances it to that installed CLI while
preserving active jobs and their timeouts. Equal/newer managers perform the
existing configuration reload. Foreground managers remain unchanged in version;
reload never starts a stopped service, downloads a release or downgrades.
Explain that an interrupted operation can have changed the service executable
before configuration acceptance. Users inspect status/the user service and retry
with the same CLI/configuration; incompatible rollback remains prohibited.
Keep the internal journal, temporary helper and native command choreography out
of public guides. Preserve manual package installation and drained backup/rollback
workflows and distinguish fixtures/builds from actual platform and job acceptance.

## Automatic configuration documentation

DinD capacity guidance must retain the released behavior through 0.2.7 and label
runner-only CPU admission as unreleased until a containing release is verified.
Explain that memory still combines runner and daemon allocations, daemon CPU
still limits its container, and existing reservations survive an upgrade until
the prior resources terminate. The 32-CPU/512-GiB example with runner 2 CPUs/16 GiB
and daemon 2 CPUs/2 GiB needs 48 CPUs for 12 idle runners before this change, and
24 CPUs/216 GiB afterward. Do not publish internal reservation records or storage
implementation details in the public guides.

Runmoor 0.2.0 introduced interactive/minimal init, omitted resource and
architecture defaults, latest runner management for Docker/Tart, resolved
configuration inspection and explicit update requests. Guides label this
version boundary and retain 0.1.3-compatible manual pins, image setup,
credentials, license terms and verification limits. Public guides describe
operator behavior, not the internal SQLite migration journal or repository
implementation paths.

The generated configuration guide shows the new scale-set, platform and
architecture labels for an ARM64 Docker host, explains the `x64` amd64 and
`macOS` Tart variants, and preserves explicitly authored label examples.

Ubuntu operator guidance distinguishes `runmoor init` from `config validate`,
locates private configuration access failures without removing existing state,
explains Docker capacity verification and non-root local-socket access, and
accounts for lingering systemd user managers retaining old group membership
after a Docker group change. It shows how to inspect an existing systemd user
unit and the original systemd failure before replacing a service definition.
The CLI README links to the corresponding public guides. No unverified
service-start root cause is claimed.

The Tart guide also describes interactive first setup from a host-supported
Apple IPSW, operator completion of the guest account/Guest Agent/tool setup,
post-reboot readiness validation, automatic runner installation and sealing,
and same-command interruption recovery. Preserve the stable Tart 2.x.x range,
exact Guest Agent version, and version-specific license boundaries. Its
published-version note must distinguish the released 0.2.0 automatic setup
from the still-unreleased guided creation of a new Mac VM.

## Host guide

Issue #1312 adds `/runmoor/host` to the shared navigation, sidebar and exact
route registry. Preserve every existing route and public-content safeguard.
The guide describes explicit `host` selection on macOS 14+ arm64, generated
routing labels, shared admission budgets without the Tart ceiling, disposable
execution state and immutable runner updates. Explain that same-account jobs
have no security boundary and require trusted workflows. Operators own tools,
shared caches, signing, Simulators, GUI sessions and escaped-daemon lifecycle.
Tart and Guest Agent are required only for Tart execution. Update overview,
configuration, commands, operations, install and CLI README together. State that
host is unreleased and actual host execution, unsigned Xcode builds and live
GitHub jobs are unvalidated; preserve existing package/Docker/Tart evidence limits.
