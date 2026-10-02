# Releases and rollback

**pnport 0.1.0 is not published.** The experimental npm next version `0.1.0-next.1` is published, with matching [signed GitHub prerelease archives](https://github.com/delinoio/oss/releases/tag/pnport%40v0.1.0-next.1). Check [installation and availability](/pnport/installation) for the registry check before testing. Stable installation and Homebrew remain unavailable.

Experimental versions use `pnport@v0.1.0-next.N`, with matching native and npm versions. They provide four macOS/glibc Linux native packages through npm next and signed GitHub prerelease archives after candidate validation. They do not change the npm latest channel or provide Homebrew. Full feature, minimum-OS and benchmark acceptance remains unfinished, and intermittent native initialization failures remain under investigation. Preview availability does not establish stable readiness or Windows support. Pin an exact published preview version when reporting failures or rolling back.

## Release readiness

A first 0.1.0 release requires validated native execution and installation on macOS and glibc Linux for x64 and arm64, covering all four targets. Windows x64 and arm64 are planned for 0.2.0 and must meet the same complete requirements before they become supported. Linux static child execution, filesystem and process conformance, complete native/package artifacts, minimum supported OS validation, benchmarks, and documentation remain release gates. A passing development fixture on one host is not complete support evidence.

Stable releases will use the `pnport@v<MAJOR.MINOR.PATCH>` identity. The published preview uses `pnport@v0.1.0-next.1`. Native and npm versions must match. Published GitHub archives include the executable and adjacent interception library, `SHA256SUMS`, and Sigstore bundles. Verify the signed checksum manifest against the release identity and exact source commit before comparing an archive's SHA-256 digest; a checksum downloaded next to an archive alone does not establish publisher identity. The npm launcher selects an exact-version native package rather than downloading, compiling, or falling back to another executable at runtime. Check [installation and availability](/pnport/installation) before using any distribution method.

## Explicit updates and rollback

pnport does not check for or install updates automatically. Update by choosing a specific published version and its matching native/npm artifacts. To roll back, explicitly install the earlier verified version, then confirm it with `pnport --version` and `pnport doctor` before restarting work. Preserve the project's Yarn installation and lockfile unless you deliberately change them.

Cache formats from different versions are designed to coexist without destructive migration. Cache cleanup is a separate explicit action; see [cache management](/pnport/cache). Compatibility of command names and diagnostic meanings is intended throughout 0.1.x; a later breaking change requires migration guidance.

Follow [Delino OSS GitHub Releases](https://github.com/delinoio/oss/releases) for actual published artifacts, and use [GitHub Issues](https://github.com/delinoio/oss/issues) for support. Preview publication does not establish stable 0.1.0 readiness.
