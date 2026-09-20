# Linux Package Repository Contract

## Ownership and scope

The CLI release workflows own native APT and DNF distribution at `https://pkgs.oss.delino.io`. `packaging/linux/` owns pinned tools, public repository configuration, and package metadata; `scripts/release/linux-packages*` owns verification, packaging, repository generation, and resumable publication. This contract complements `repository-workflow-contract.md`.

Stable packages are binpm, cargo-mono, nodeup, with-watch, and derun. Runmoor remains preview-only. Both amd64/x86_64 and arm64/aarch64 are required. Arch, Alpine, TTL, and DevHud are excluded. Public package names equal executable names. Version is the exact source SemVer with packaging revision 1. Installation owns `/usr/bin/<project>` and package documentation only; it never initializes user configuration, downloads runtimes, registers services, or starts a daemon.

## Build and input trust

Rust GNU Linux binaries use the checksum-pinned AlmaLinux 9 image and the repository Rust toolchain. Dynamic GLIBC requirements must not exceed 2.34; only explicitly mapped runtime libraries are allowed. x86-64 builds target the baseline ISA and ARM64 builds target generic ARMv8. Derun and Runmoor Linux builds disable CGO. Existing GitHub asset names and Sigstore identities remain authoritative.

Only releases whose source contains this package-distribution contract can enter the repository. Resolve the exact tag to its commit, validate source version and channel, and verify the checksum manifest and both Linux archive Sigstore bundles against the originating release workflow, exact certificate commit SHA, repository and GitHub Actions OIDC issuer. Do not backfill older releases, rebuild a published version, overwrite an existing package identity, or expose signing authority to unverified archives.

## Repository publication

`delino-oss-packages` is the public R2 bucket. `delino-oss-packages-state` is private and stores immutable candidate records, packages, and publication journals. Recreate aptly databases from the recorded package inventory rather than depending on runner caches.

APT exposes `/apt`, suites `stable` and `preview`, component `main`, and both Debian architecture names. Publish package objects and by-hash indexes before replacing signed `InRelease`. Retain old by-hash objects. Supported clients use InRelease; detached mutable Release/signature pairs are not public entrypoints.

DNF exposes per-channel and per-architecture mirrorlists pointing at immutable repository snapshots. Sign both RPM packages and `repomd.xml`; require `gpgcheck=1` and `repo_gpgcheck=1`. Promote a mirrorlist only after every snapshot object is uploaded and verified. Retain previous snapshots. Stable registration must not enable preview; preview registration is explicit and persists for subsequent package-manager updates.

One GitHub concurrency group serializes all publishers with `cancel-in-progress: false` and `queue: max`. Publication journals make retries reuse complete signed bytes and recover each incomplete repository promotion. An older request must never remove a newer version or another project's packages. No automatic garbage collection is implemented in v1.

Cache immutable package and snapshot objects for one year. Bypass caching for mutable repository entrypoints and setup/key files, and disable negative caching for this hostname. Verify public HTTPS responses before reporting publication success. Structured logs contain project, version, revision, phase and stable error codes, never credentials or signing material.

## Credentials and operations

The `linux-packages` GitHub Environment owns dedicated R2 object credentials limited to the two buckets and an RSA 4096 OpenPGP signing subkey. Keep the primary secret key and recovery material outside CI. Commit the public certificate and fingerprint only. APT uses a repository-specific Signed-By keyring. PR and dry-run paths use disposable signing identities and cannot consume production secrets or write R2.

Restrict the environment to main and the six CLI release-tag patterns. Provision the buckets, custom domain, HTTPS and cache rules before enabling production publication. The rollout operator must wait for the exact downstream package publication and public verification after Release Project finishes its asynchronous tag handoff. Recovery retries must target the exact release identity and never modify GitHub tags or existing release assets.

## Validation and rollout

The acceptance matrix is Ubuntu 22.04/24.04/26.04 LTS, Debian 12/13, Fedora 43/44, UBI 9/10, Rocky Linux 9/10, and AlmaLinux 9/10 on both architectures. Verify repository registration, signature validation, installation, upgrade, version/help execution, dependency resolution, removal and preservation of user data. Container checks do not certify Runmoor's Docker/Tart service integration.

Rocky Linux 10 uses the project's `rockylinux/rockylinux:10` image; the separate Docker Official Image namespace does not publish `rockylinux:10`. Installation logs identify the exact distribution image, project, architecture and fixture/live mode.

Test incorrect identities, checksums, signatures, architectures, dependencies and channels; interrupted uploads; concurrent publication requests; retry without replacement; stale indexes; and partial APT/DNF promotion. Tests use isolated files, keys and object stores. Run workflow syntax/contracts, release fixtures and changed documentation-app tests. Remove generated dist directories before completion.

Changes to any of the four Rust CLI sources, workspace Cargo inputs, Cargo configuration or Rust toolchain select the native package CI job. Both architectures must rebuild against AlmaLinux 9 and pass ELF compatibility inspection before merging these changes.

First public releases follow the existing manual coordinator, one patch release at a time: binpm, nodeup, cargo-mono, with-watch, derun, runmoor. Verify each public package before continuing. Deployment is complete only when every project is installable on both architectures from its intended channel.

## Implemented operations

`packaging/linux/pins.json` pins nFPM 2.47.0, aptly 1.6.3, createrepo_c 1.2.4, the AlmaLinux build image and the Node-based tools image. `Tools.Dockerfile` verifies tool archives before installation. `scripts/ci/workflows.mjs` validates the one exact `queue: max` declaration before masking only that line for actionlint 1.7.12; remove the adapter when the pinned linter supports the field.

The committed public key's primary fingerprint is `B08DE37A14DD10DDFD04E66E87CB82A1F70FBD30`. CI receives only the encrypted signing subkey export. The operator owns the offline primary certificate, revocation material and passphrase backup. The current signing subkey expires after two years; rotate the signing subkey and committed public certificate before expiry while preserving the primary fingerprint. A primary-key replacement requires an explicitly documented user trust migration.

The protected Environment contains secrets `LINUX_PACKAGES_R2_ACCESS_KEY_ID`, `LINUX_PACKAGES_R2_SECRET_ACCESS_KEY`, `LINUX_PACKAGES_SIGNING_SUBKEY` (base64), and `LINUX_PACKAGES_SIGNING_PASSPHRASE`; non-secret variables are `LINUX_PACKAGES_R2_ACCOUNT_ID` and `LINUX_PACKAGES_SIGNING_FINGERPRINT`. R2 credentials have Object Read & Write on exactly the public and state buckets, without bucket-administration permissions. The custom domain has minimum TLS 1.2; the managed r2.dev URL stays disabled and the state bucket has no public domain.

Cloudflare's `Delino packages immutable objects` cache rule matches only this hostname and `/apt/pool/`, `/by-hash/`, or `/snapshots/` paths. It makes those responses eligible for caching, honors origin cache-control with bypass when absent, and sets HTTP status codes 400 and above to No store. The complementary `Delino packages mutable entrypoints` rule bypasses caching for every other path on the same hostname. Do not extend either rule to other domains. Both rules were activated during provisioning; public compressed-path 404 probes return `CF-Cache-Status: BYPASS`.

The reusable `release-linux-packages.yml` validates exact project/version/revision input, builds and tests temporary signed repositories on all thirteen images and both native runner architectures, and only then enters the protected publisher. The public installation matrix follows publication. Manual execution defaults to `dry-run`; `publish` retries only an already-published supported release.

Private state contains immutable package objects by SHA-256, signed candidate records by project/version, a compare-and-swap catalog, signed complete snapshot descriptors, immutable publication receipts, and content-addressed structured history events. Snapshot generations use the catalog's SHA-256 because they identify content rather than a persisted entity. History identifiers use the event hash to preserve exact event bytes. Snapshot and candidate signatures are verified before reuse. Reconstruct aptly from all catalog packages. Never delete a candidate or alter its identity to repair a failed run: correct the external failure and retry the same release. Already uploaded identical files are accepted, conflicting immutable bytes fail, and a stale catalog generation cannot be promoted.

GNU tar-based legacy release fixtures must run in Linux when the local host only provides BSD tar. Temporary package tests use certification-only primary keys plus signing subkeys, matching CI's secret-key boundary. The shell fixtures verify package-manager lifecycle; release-time fixtures additionally inspect and execute the actual verified release binaries. Rust source is unchanged by the compatibility build migration.

RPM packages are generated by nFPM and signed with `rpmsign`/GnuPG. nFPM 2.47.0's key reader decrypts subkeys only inside its encrypted-primary-key branch, which skips a CI export's dummy primary and leaves its signing subkey encrypted. The bounded adapter preserves the subkey-only secret boundary; remove it when upstream supports this export and the encrypted-key fixtures pass through nFPM signing. Tests deliberately use passphrase-protected certification-only primaries and signing-subkey exports to exercise this case.
