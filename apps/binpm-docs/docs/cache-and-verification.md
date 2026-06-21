# Cache and Verification

`~/.binpm/cache` is the user-level global asset cache shared by all binpm installs for the same account.

## Cache Reuse

Cache reuse is validated with the strongest available integrity source:

- Provider asset digest.
- Upstream checksum sidecar.
- Upstream checksum manifest.
- Successfully verified signature under a documented trust policy.
- Locally recorded SHA-256 metadata when stronger upstream material is unavailable.

Checksum sidecar discovery, checksum manifest discovery, and signature verification remain implementation work. Current installs rely on provider digests when available or locally recorded SHA-256 metadata with a warning.

Cache hits are revalidated before extraction or install finalization. If cache revalidation fails, binpm discards the corrupted entry and redownloads the asset.

## Cache Commands

`binpm cache list` reports cached assets.

`binpm cache prune` removes cached assets that are no longer needed by installed tools.

`binpm cache clean` removes cached assets while preserving installed tools and executable links or copies under `~/.binpm/bin`.

`binpm cache key` prints a stable CI cache key derived from the current target and `binpm.lock`; it does not download, install, or populate cache entries.

## Verification

Installs without upstream checksum material or successfully verified signature material continue with an explicit warning and locally recorded SHA-256 metadata.

`--require-verified` and `binpm verify --require-verified` fail when no trusted provider digest is available. Checksum sidecar discovery, checksum manifest discovery, and signature verification remain implementation work.
