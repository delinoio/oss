# Releases, verification, and rollback

**0.1.0 is not published.** pnport will publish only after all six native execution and installation targets pass. There is no partial preview release or fixed release date. Check [GitHub releases](https://github.com/delinoio/oss/releases?q=pnport) and [npm](https://www.npmjs.com/package/@delino/pnport) for actual availability.

Published tags use `pnport@v<version>`. Each target has a native archive with its executable and adjacent interception library. A release also carries `SHA256SUMS` and Sigstore bundles. Verify the selected archive and checksum manifest with a trusted Cosign installation, the `release-pnport.yml` GitHub Actions signing identity, GitHub's OIDC issuer, and the independently resolved exact source commit. Then compare the archive digest with the authenticated checksum entry. A checksum downloaded beside an archive is not by itself proof of publisher identity.

The npm launcher and six optional native packages use one exact version. Published npm packages carry provenance. Keep optional dependencies enabled and commit the package-manager lockfile when selecting a version. Homebrew selects prebuilt macOS or GNU Linux archives; no source-build fallback is part of the formula. Direct installers verify the archive before activating a version.

Updates and rollback are explicit: install the desired published version again, verify `pnport --version`, and restart long-running tools. Cache formats from distinct versions coexist; pnport does not automatically migrate or erase newer entries. There is no self-update check. Cold and warm execution, filesystem, memory, and disk benchmark measurements will be published with release evidence; no performance threshold or number is claimed before validation.
