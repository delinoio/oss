# Releases and rollback

**pnport 0.1.2 is available.** npm latest, [signed GitHub release archives](https://github.com/delinoio/oss/releases/tag/pnport%40v0.1.2), the POSIX installer and Homebrew provide the four macOS/glibc Linux builds. Check [installation and known limits](/pnport/installation) before use. The experimental npm next version `0.1.0-next.1` and its signed prerelease archives remain unchanged.

Experimental versions use `pnport@v0.1.0-next.N`, with matching native and npm versions. They provide four macOS/glibc Linux native packages through npm next and signed GitHub prerelease archives after candidate validation. They do not change the npm latest channel or provide Homebrew. Full feature, minimum-OS and benchmark acceptance remains unfinished, and intermittent native initialization failures remain under investigation. Preview availability does not establish stable readiness or Windows support. Pin an exact published preview version when reporting failures or rolling back.

## Changes in 0.1.2

Hidden tool caches, including Vitest's default `node_modules/.vite` cache, can coexist with virtual dependencies in a writable project. Dependency folders remain virtual and read-only; actual package folders still cause a conflict. Earlier 0.1.0 and 0.1.0-next.1 packages retain their cache-only conflict behavior.

On macOS, an optional child probe can recover from `ENOTSUP` when pnport rejects a protected or non-injectable executable before launch. The rejected image never runs or gets replaced. Root-command rejection, privileged images and injection failures retain their failure boundaries. These changes preserve the known initialization and cancellation limits below.

## Published validation and known limits

The published 0.1.2 build passed native execution, npm/Yarn and direct installation, inline/split TypeScript and numerical benchmark checks on macOS 15 and Ubuntu 22.04 for x64 and arm64, including Linux static children. Complete filesystem/process/tool acceptance remains incomplete. Intermittent macOS initialization failures and cancellation returning 125 instead of the signal-derived status remain unresolved. A successful fixture does not establish universal executable compatibility. Windows x64 and arm64 remain planned for 0.2.0 and must meet the same complete requirements before they become supported.

Stable releases use the `pnport@v<MAJOR.MINOR.PATCH>` identity. The published preview uses `pnport@v0.1.0-next.1`. Native and npm versions must match. Published GitHub archives include the executable and adjacent interception library, `SHA256SUMS`, and Sigstore bundles. Verify the signed checksum manifest against the release identity and exact source commit before comparing an archive's SHA-256 digest; a checksum downloaded next to an archive alone does not establish publisher identity. The npm launcher selects an exact-version native package rather than downloading, compiling, or falling back to another executable at runtime. Check [installation and availability](/pnport/installation) before using any distribution method.

## Explicit updates and rollback

pnport does not check for or install updates automatically. Update by choosing a specific published version and its matching native/npm artifacts. To roll back, explicitly install the earlier verified version, then confirm it with `pnport --version` and `pnport doctor` before restarting work. Preserve the project's Yarn installation and lockfile unless you deliberately change them.

Cache formats from different versions are designed to coexist without destructive migration. Cache cleanup is a separate explicit action; see [cache management](/pnport/cache). Compatibility of command names and diagnostic meanings is intended throughout 0.1.x; a later breaking change requires migration guidance.

Follow [Delino OSS GitHub Releases](https://github.com/delinoio/oss/releases) for actual published artifacts, and use [GitHub Issues](https://github.com/delinoio/oss/issues) for support. Preview publication does not establish complete stable acceptance.
