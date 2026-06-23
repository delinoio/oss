# Reference

Use this page as a compact contract reference for stable source syntax, target values, and read-only command behavior. For installation-channel availability, start with the first-party platform matrix before applying the broader target parsing model to third-party release assets.

## Stable Source Identifiers

- `github:owner/repo[@version]`
- `github:<host>/owner/repo[@version]`
- `gitlab:<host>/<namespace...>/<project>[@version]`

`@version` is an exact release tag request. Omit it to select the latest stable release. `@latest`, SemVer range-like selectors, channel selectors such as `@beta`, and numeric major-version pins such as `@1` are rejected with diagnostics.

GitHub.com shorthand input is accepted for ergonomics:

- `owner/repo[@version]`
- `https://github.com/owner/repo`
- `https://github.com/owner/repo/releases/download/<tag>/<asset>`

binpm normalizes those inputs to canonical `github:` source strings before writing manifests, lockfiles, package records, or JSON diagnostics. GitLab URL shorthands and arbitrary direct URLs are not source identifiers; use canonical `gitlab:<host>/<namespace...>/<project>[@version]` instead.

## Target Model

binpm resolves release assets against the current host target:

- OS: `linux`, `darwin`, `windows`, `freebsd`
- CPU architecture: `x86_64`, `aarch64`, `i686`, `armv7`
- Libc or ABI environment: `gnu`, `musl`, `msvc`, `any`

Unsupported operating systems or CPU architectures fail clearly instead of being mapped to a supported fallback target.

CPU feature tokens such as `baseline` and `modern` are scored separately from architecture tokens. Baseline variants are preferred for automatic selection. Modern variants require explicit host CPU capability support, so binpm reports them as a compatibility decision instead of treating `modern` as an architecture.

The target model is broader than binpm's own first-party release matrix. Direct installers, Homebrew, and cargo-binstall publish or consume binpm prebuilt assets only for the documented first-party macOS, Linux, and Windows x64/arm64 platforms; additional target values are for parsing and scoring third-party package assets or writing explicit local target overrides.

## GitLab HTTPS Assets

GitLab release links must use HTTPS for the release link URL, the direct asset URL when present, and the final redirect target. `binpm explain` reports those cases separately so maintainers can fix the GitLab release link or publish a secure direct asset URL. Redirect diagnostics show only a sanitized origin and omit credentials, query strings, and fragments.

Generated source archives are not binary package assets. GitHub source downloads, GitLab `assets.sources`, and files such as `source.tar.gz` or `source.zip` are reported as ignored source archives and are never selected automatically or through target overrides. Releases need a portable prebuilt archive or bare executable for the target.

## Global Update Status

`binpm update [cmd...] [--local|--global] [--dry-run]` updates selected tools or all tools in the selected scope. Omitting command names is explicit all-tools mode; output states that mode before the planned update list, and `--dry-run` previews it without mutation. Global updates use existing global package records, preserve each command alias and selected upstream binary, resolve the latest stable release for the recorded source, and finalize through the same cache, install, rollback, and verification behavior as global installs.
