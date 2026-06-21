# Cache and Verification

`~/.binpm/cache` is the user-level global asset cache shared by all binpm installs for the same account.

## Cache Reuse

Cache reuse must be validated with the strongest available integrity source:

- Provider asset digest.
- Upstream checksum sidecar.
- Upstream checksum manifest.
- Successfully verified signature under a documented trust policy.
- Locally recorded SHA-256 metadata when stronger upstream material is unavailable.

Cache hits must be revalidated before extraction or install finalization. If cache revalidation fails, binpm must discard the corrupted entry and redownload the asset.

## Cache Commands

`binpm cache list` reports cached assets and whether installed package manifests reference each entry.

`binpm cache prune` removes only entries not referenced by installed package manifests or the user-level local-project cache reference index.

`binpm cache clean` removes cache entries while preserving installed package records and executable links or copies under `~/.binpm/bin`.

`binpm cache key` prints a stable CI cache key derived from the current target and `binpm.lock`; it must not download, install, modify package records, or populate cache entries.

## Verification

Installs without upstream checksum material or successfully verified signature material are allowed in v1 only with an explicit warning and locally recorded SHA-256 metadata.

`--require-verified` and `binpm verify --require-verified` must fail when no trusted provider digest, upstream checksum sidecar, upstream checksum manifest, or successfully verified signature is available.
