# Cache and Verification

`~/.binpm/cache` is the user-level global asset cache shared by all binpm installs for the same account.

## Cache Reuse

Cache reuse is validated with the strongest available integrity source:

- Provider asset digest.
- Upstream checksum sidecar.
- Upstream checksum manifest.
- Successfully verified signature under a documented trust policy.
- Locally recorded SHA-256 metadata when stronger upstream material is unavailable.

When provider metadata exposes a trusted SHA-256 digest, binpm verifies the downloaded asset against that digest first. If no provider digest is available, binpm looks for upstream checksum sidecars such as `<asset>.sha256` and checksum manifests such as `SHA256SUMS` or `checksums.txt`, then verifies the selected asset against the matching SHA-256 entry. Signature verification remains implementation work.

Cache hits are revalidated before extraction or install finalization. If cache revalidation fails, binpm discards the corrupted entry and redownloads the asset.

## Cache Commands

`binpm cache list` reports cached assets.

`binpm cache prune` removes stale structured local-project cache references, then removes cached assets that are no longer needed by installed tools or active project references. Legacy plain-text project references remain preserving until a future install or removal rewrites them.

`binpm cache clean` removes cached asset entries under `~/.binpm/cache/sha256`. It preserves the project-reference index under `~/.binpm/cache/refs`, installed package records, and executable links or copies under `~/.binpm/bin`; command output states those removed and preserved boundaries.

`binpm cache key` prints a stable CI cache key derived from the current target and `binpm.lock`; it does not download, install, or populate cache entries. When `binpm.lock` is absent, human output warns that the empty lockfile digest is used. `binpm cache key --json` reports the same key with `lockfile` status.

`binpm doctor` reports stale and legacy cache-reference counts without repairing them. Run `binpm cache prune` to remove stale structured references and then prune unreferenced cache entries.

## Verification

Installs without provider digest, upstream checksum material, or successfully verified signature material continue with an explicit warning and locally recorded SHA-256 metadata.

`--require-verified` and `binpm verify --require-verified` fail when no trusted provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified signature is available. Signature verification remains implementation work, so raw signature sidecars do not satisfy strict verification today.
