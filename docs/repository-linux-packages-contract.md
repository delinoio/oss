# Linux Package Repository Contract

## Ownership and scope

The CLI release workflows own native APT and DNF distribution at `https://pkgs.oss.delino.io`. `packaging/linux/` owns pinned tools, public repository configuration, and package metadata; `scripts/release/linux-packages*` owns verification, packaging, repository generation, and resumable publication. This contract complements `repository-workflow-contract.md`.

The stable enrollment contract covers binpm, cargo-mono, nodeup, with-watch, derun, runmoor, and clibox. Preview is reserved with no enrolled CLI. No native packages have been published; the initial rollout, including binpm, was canceled before registry or tag publication. Integrations apply to future explicitly requested releases. Enrollment does not imply a package is already published. Both amd64/x86_64 and arm64/aarch64 are required. Arch Linux and Alpine Linux are unsupported, and DevHud is not enrolled in native CLI packaging. Public package names equal executable names. Version is the exact source SemVer with packaging revision 1. CLI installation owns `/usr/bin/<project>` and package documentation, plus the shared APT keyring dependency described below; it never initializes user configuration, downloads runtimes, registers services, or starts a daemon.

All seven originating GitHub workflows publish stable releases and feed the stable native repository. Project contracts determine channels; callers cannot choose a channel. Clibox reuses the exact verified GNU npm executable bytes in its two signed GitHub Release archives before entering this common workflow.

## CI validation

The `linux-packages` CI job runs compatibility builds, disposable signed repositories, and package lifecycle checks on native amd64/arm64 hosts for relevant main pushes and every manual dispatch. PRs skip both architectures, including when CI configuration changes force all eligible checks, to keep package builds and distribution installation matrices out of the PR feedback path. The shared planner marks this job as native packaging; `CI Result` retains its dependency and requires the planned skip on PRs and success when selected on main or manually. General Linux tests, OCI checks, and lightweight static package contracts remain eligible on PRs. Release workflows retain their full package build, installation, and publication validation.

## Build and input trust

Rust GNU Linux binaries use the checksum-pinned AlmaLinux 9 image and the repository Rust toolchain. Bootstrap rustup 1.29.1 from the architecture-specific official archive URL in `pins.json`, verify its committed SHA-256 before execution, and disable self-updates. Never execute the mutable `sh.rustup.rs` bootstrap. Dynamic GLIBC requirements must not exceed 2.34; only explicitly mapped runtime libraries are allowed. x86-64 builds target the baseline ISA and ARM64 builds target generic ARMv8. Derun and Runmoor Linux builds disable CGO. Existing GitHub asset names and Sigstore identities remain authoritative.

Only releases whose source contains this package-distribution contract can enter the repository. Resolve the exact tag to its commit, validate source version and channel, and verify the checksum manifest and both Linux archive Sigstore bundles against the originating release workflow, exact certificate commit SHA, repository and GitHub Actions OIDC issuer. Do not backfill older releases, rebuild a published version, overwrite an existing package identity, or expose signing authority to unverified archives.

## Repository publication

`delino-oss-packages` is the public R2 bucket. `delino-oss-packages-state` is private and stores immutable candidate records, packages, and publication journals. Recreate aptly databases from the recorded package inventory rather than depending on runner caches.

APT exposes `/apt`, suites `stable` and `preview`, component `main`, and both Debian architecture names. Publish package objects and by-hash indexes before replacing signed `InRelease`. Retain old by-hash objects. The publisher must copy only content-addressed hash leaves from aptly's by-hash output, never its changing named symlink aliases. Supported clients use InRelease; detached mutable Release/signature pairs are not public entrypoints.

DNF exposes per-channel and per-architecture mirrorlists pointing at immutable repository snapshots. Sign both RPM packages and `repomd.xml`; require `gpgcheck=1` and `repo_gpgcheck=1`. Promote a mirrorlist only after every snapshot object is uploaded and verified. Retain previous snapshots. Stable registration must not enable preview; preview registration is explicit and persists for subsequent package-manager updates.

One GitHub concurrency group serializes all publishers with `cancel-in-progress: false` and `queue: max`. Publication journals make retries reuse complete signed bytes and recover each incomplete repository promotion. An older request must never remove a newer version or another project's packages. No automatic garbage collection is implemented in v1.

Cache immutable package and snapshot objects for one year. Bypass caching for mutable repository entrypoints and setup/key files, and disable negative caching for this hostname. Verify public HTTPS responses before reporting publication success. Structured logs contain project, version, revision, phase and stable error codes, never credentials or signing material.

## Credentials and operations

The `linux-packages` GitHub Environment owns dedicated R2 object credentials limited to the two buckets and an RSA 4096 OpenPGP signing subkey. Keep the primary secret key and recovery material outside CI. Commit the public certificate and fingerprint only. APT uses a repository-specific Signed-By keyring. PR and dry-run paths use disposable signing identities and cannot consume production secrets or write R2.

Restrict the environment to main and the seven CLI release-tag patterns, including `clibox@v*`. Each release caller explicitly uses `secrets: inherit`: without it, the hosted reusable-workflow runner can resolve environment variables but return empty environment secrets (actions/runner#4453). Only the guarded publisher references production credentials; validation and installation jobs do not consume them. Remove this workaround only after the upstream behavior is corrected and protected environment access is verified. Provision the buckets, custom domain, HTTPS and cache rules before enabling production publication. The rollout operator must wait for the exact downstream package publication and public verification after Release Project finishes its asynchronous tag handoff. Recovery retries must target the exact release identity and never modify GitHub tags or existing release assets. If an immutable source tag contains a broken reusable-workflow call, dispatch `release-linux-packages.yml` from main with the already-published project/version/revision and `mode=publish`; it repeats the same verification and installation gates before accessing the protected environment.

## Validation and rollout

The acceptance matrix is Ubuntu 22.04/24.04/26.04 LTS, Debian 12/13, Fedora 43/44, UBI 9/10, Rocky Linux 9/10, and AlmaLinux 9/10 on both architectures. Verify repository registration, signature validation, installation, upgrade, version/help execution, dependency resolution, removal and preservation of user data. Container checks do not certify Runmoor's Docker/Tart service integration.

Rocky Linux 10 uses the project's `rockylinux/rockylinux:10` image; the separate Docker Official Image namespace does not publish `rockylinux:10`. Installation logs identify the exact distribution image, project, architecture and fixture/live mode.

Test incorrect identities, checksums, signatures, architectures, dependencies and channels; interrupted uploads; concurrent publication requests; retry without replacement; stale indexes; and partial APT/DNF promotion. Tests use isolated files, keys and object stores. Run workflow syntax/contracts, release fixtures and changed documentation-app tests. Remove generated dist directories before completion.

Relevant main changes to any of the five Rust CLI sources, workspace Cargo inputs, Cargo configuration or Rust toolchain select the native package CI job. Both architectures rebuild against AlmaLinux 9 and pass ELF compatibility inspection on main and manual runs; PRs retain the planned native-package skip described above. Release-time validation still requires both architectures before publication.

No release dispatch or retry is authorized by this implementation update. The canceled binpm coordinator left its source version commit on main but created no release tag or public packages. Future explicitly requested releases use the existing coordinator and the same per-project public verification gate. Public documentation must mark unpublished CLI examples as unavailable until their own public installation checks pass.

## Implemented operations

`packaging/linux/pins.json` pins nFPM 2.47.0, aptly 1.6.3, createrepo_c 1.2.4, the AlmaLinux build image and the Node-based tools image. `Tools.Dockerfile` verifies tool archives before installation. `scripts/ci/workflows.mjs` validates the one exact `queue: max` declaration before masking only that line for actionlint 1.7.12; remove the adapter when the pinned linter supports the field.

The committed public key's primary fingerprint is `B08DE37A14DD10DDFD04E66E87CB82A1F70FBD30`. CI receives only the encrypted signing subkey export. The operator owns the offline primary certificate, revocation material and passphrase backup. The current signing subkey expires after two years; rotate the signing subkey and committed public certificate before expiry while preserving the primary fingerprint. A primary-key replacement requires an explicitly documented user trust migration.

The protected Environment contains secrets `LINUX_PACKAGES_R2_ACCESS_KEY_ID`, `LINUX_PACKAGES_R2_SECRET_ACCESS_KEY`, `LINUX_PACKAGES_SIGNING_SUBKEY` (base64), and `LINUX_PACKAGES_SIGNING_PASSPHRASE`; non-secret variables are `LINUX_PACKAGES_R2_ACCOUNT_ID` and `LINUX_PACKAGES_SIGNING_FINGERPRINT`. R2 credentials have Object Read & Write on exactly the public and state buckets, without bucket-administration permissions. The custom domain has minimum TLS 1.2; the managed r2.dev URL stays disabled and the state bucket has no public domain.

Cloudflare's `Delino packages immutable objects` cache rule matches only this hostname and `/apt/pool/`, `/by-hash/`, or `/snapshots/` paths. It makes those responses eligible for caching, honors origin cache-control with bypass when absent, and sets HTTP status codes 400 and above to No store. The complementary `Delino packages mutable entrypoints` rule bypasses caching for every other path on the same hostname. Do not extend either rule to other domains. Both rules were activated during provisioning; public compressed-path 404 probes return `CF-Cache-Status: BYPASS`.

The reusable `release-linux-packages.yml` validates exact project/version/revision input, builds and tests temporary signed repositories on all thirteen images and both native runner architectures, and only then enters the protected publisher. The public installation matrix follows publication. Manual execution defaults to `dry-run`; `publish` retries only an already-published supported release.

Private state contains immutable package objects by SHA-256, signed candidate records by project/version, a compare-and-swap catalog, signed complete snapshot descriptors, immutable publication receipts, and content-addressed structured history events. Snapshot generations use the catalog's SHA-256 because they identify content rather than a persisted entity. History identifiers use the event hash to preserve exact event bytes. Snapshot and candidate signatures are verified before reuse. Reconstruct aptly from all catalog packages. Never delete a candidate or alter its identity to repair a failed run: correct the external failure and retry the same release. Already uploaded identical files are accepted, conflicting immutable bytes fail, and a stale catalog generation cannot be promoted.

GNU tar-based legacy release fixtures must run in Linux when the local host only provides BSD tar. Temporary package tests use certification-only primary keys plus signing subkeys, matching CI's secret-key boundary. The shell fixtures verify package-manager lifecycle; release-time fixtures additionally inspect and execute the actual verified release binaries. Rust source is unchanged by the compatibility build migration.

RPM packages are generated by nFPM and signed with `rpmsign`/GnuPG. nFPM 2.47.0's key reader decrypts subkeys only inside its encrypted-primary-key branch, which skips a CI export's dummy primary and leaves its signing subkey encrypted. The bounded adapter preserves the subkey-only secret boundary; remove it when upstream supports this export and the encrypted-key fixtures pass through nFPM signing. Tests deliberately use passphrase-protected certification-only primaries and signing-subkey exports to exercise this case.

Signing-key import must prove that an actual signature verifies in an isolated keyring containing only the committed public certificate. Matching primary fingerprints is insufficient after subkey rotation. All subsequent signature checks use that same public-only keyring.

All repository-owned Linux packages, including the keyring, declare Apache-2.0 and include its complete text under each package's documentation directory. Third-party source bundled by a product retains its separate notices; already-published immutable package versions retain their original metadata.

Public registration instructions write the complete source configuration from the documentation with quoted heredocs. They must preserve the literal DNF `$basearch` variable, APT Signed-By and both DNF signature checks. Downloaded repository configuration must not become the authority for enabling signature verification: storage-write credentials are separate from signing authority. The generated setup files remain available for controlled publisher/fixture validation, while users bootstrap from the documentation's explicit configuration after checking the public key fingerprint.


## APT certificate updates and signing-subkey rotation

The auxiliary `delino-archive-keyring` package is architecture `all`, is present in both suites, and owns `/usr/share/keyrings/delino-packages.gpg` as package data rather than a conffile. Each CLI `.deb` depends on it. It has no scripts, services or source-configuration ownership and is not an additional CLI release-project input. Bootstrap the same path only after checking the public primary fingerprint. Normal APT upgrades then deliver certificate updates through the already authenticated repository, including to preview-only users.

Increase `packaging/linux/signing.json`'s integer `keyring_version` whenever the committed certificate changes. A version permanently identifies one certificate and one immutable `.deb`; retain the public portions of every historical signing subkey so old candidates and snapshots remain verifiable. The catalog includes the certificate digest, keyring version and active subkey, so retrying an already published CLI can publish a certificate update without repackaging that CLI. Private state retains signed `keyrings/<version>.json` records and their content-addressed package objects.

For rotation, add the successor signing subkey using the offline primary, commit the expanded certificate and increased keyring version, and publish an existing exact CLI release using the old CI signer. The publisher records the first fully read-back-verified public completion for that certificate. Keep the old signer for at least 30 days to let existing clients upgrade their keyring. Only then replace CI's encrypted subkey export with the single successor signing subkey and retry the exact CLI publication. The gate rejects a same-step certificate/signer change, an unstaged signer, an incomplete publication, a shorter overlap, rollback and removal of historical public subkeys. The secret import rejects primary secret material and ambiguous multiple available signing subkeys and selects the exact permitted subkey for every signature.

An offline client that misses the overlap or remains offline beyond expiry must repeat the documented fingerprint-verified key bootstrap; signature verification remains mandatory. A primary fingerprint replacement is a separate explicit trust migration. Tests use temporary old/new subkeys and an isolated APT/dpkg client to install keyring v1, upgrade to v2 through old-key-signed metadata, verify successor-signed metadata, and reject that metadata with the stale keyring. Timing, incomplete-promotion and immutable-version gates have separate temporary-state tests.

clibox packages require `ca-certificates` in both formats for OS-trusted HTTPS readiness checks. Preserve Rustls/ring C compilation and the pinned GNU/musl link boundaries; ELF-derived dependencies alone cannot represent certificate data.
