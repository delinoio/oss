# Linux Package Repository Contract

## Ownership and scope

The CLI release workflows own native APT and DNF distribution at `https://pkgs.oss.delino.io`. `packaging/linux/` owns pinned tools, public repository configuration, and package metadata; `scripts/release/linux-packages*` owns verification, packaging, repository generation, and resumable publication. This contract complements `repository-workflow-contract.md`.

Stable packages are binpm, cargo-mono, nodeup, with-watch, and derun. Runmoor remains preview-only. Both amd64/x86_64 and arm64/aarch64 are required. Arch, Alpine, TTL, and DevHud are excluded. Public package names equal executable names. Version is the exact source SemVer with packaging revision 1. Installation owns `/usr/bin/<project>` and package documentation only; it never initializes user configuration, downloads runtimes, registers services, or starts a daemon.

## Build and input trust

Rust GNU Linux binaries use the checksum-pinned AlmaLinux 9 image and the repository Rust toolchain. Dynamic GLIBC requirements must not exceed 2.34; only explicitly mapped runtime libraries are allowed. x86-64 builds target the baseline ISA and ARM64 builds target generic ARMv8. Derun and Runmoor Linux builds disable CGO. Existing GitHub asset names and Sigstore identities remain authoritative.

Only releases whose source contains this package-distribution contract can enter the repository. Resolve the exact tag to its commit, validate source version and channel, and verify the checksum manifest and both Linux archive Sigstore bundles against the originating release workflow and GitHub Actions OIDC issuer. Do not backfill older releases, rebuild a published version, overwrite an existing package identity, or expose signing authority to unverified archives.

## Repository publication

`delino-oss-packages` is the public R2 bucket. `delino-oss-packages-state` is private and stores immutable candidate records, packages, and publication journals. Recreate aptly databases from the recorded package inventory rather than depending on runner caches.

APT exposes `/apt`, suites `stable` and `preview`, component `main`, and both Debian architecture names. Publish package objects and by-hash indexes before replacing signed `InRelease`. Retain old by-hash objects. Supported clients use InRelease; detached mutable Release/signature pairs are not public entrypoints.

DNF exposes per-channel and per-architecture mirrorlists pointing at immutable repository snapshots. Sign both RPM packages and `repomd.xml`; require `gpgcheck=1` and `repo_gpgcheck=1`. Promote a mirrorlist only after every snapshot object is uploaded and verified. Retain previous snapshots. Stable registration must not enable preview; preview registration is explicit and persists for subsequent package-manager updates.

One GitHub concurrency group serializes all publishers with `cancel-in-progress: false` and `queue: max`. Publication journals make retries reuse complete signed bytes and recover each incomplete repository promotion. An older request must never remove a newer version or another project's packages. No automatic garbage collection is implemented in v1.

Cache immutable package and snapshot objects for one year. Bypass caching for mutable repository entrypoints and setup/key files, and disable negative caching for this hostname. Verify public HTTPS responses before reporting publication success. Structured logs contain project, version, revision, phase and stable error codes, never credentials or signing material.

## Credentials and operations

The `linux-packages` GitHub Environment owns dedicated R2 object credentials limited to the two buckets and an RSA 4096 OpenPGP signing subkey. Keep the primary secret key and recovery material outside CI. Commit the public certificate and fingerprint only. APT uses a repository-specific Signed-By keyring. PR and dry-run paths use disposable signing identities and cannot consume production secrets or write R2.

Restrict the environment to main and the six CLI release-tag patterns. Provision the buckets, custom domain, HTTPS and cache rules before enabling production publication. Public release coordinators must wait for package publication and its public verification. Recovery retries must target the exact release identity and never modify GitHub tags or existing release assets.

## Validation and rollout

The acceptance matrix is Ubuntu 22.04/24.04/26.04 LTS, Debian 12/13, Fedora 43/44, UBI 9/10, Rocky Linux 9/10, and AlmaLinux 9/10 on both architectures. Verify repository registration, signature validation, installation, upgrade, version/help execution, dependency resolution, removal and preservation of user data. Container checks do not certify Runmoor's Docker/Tart service integration.

Test incorrect identities, checksums, signatures, architectures, dependencies and channels; interrupted uploads; concurrent publication requests; retry without replacement; stale indexes; and partial APT/DNF promotion. Tests use isolated files, keys and object stores. Run workflow syntax/contracts, release fixtures and changed documentation-app tests. Remove generated dist directories before completion.

First public releases follow the existing manual coordinator, one patch release at a time: binpm, nodeup, cargo-mono, with-watch, derun, runmoor. Verify each public package before continuing. Deployment is complete only when every project is installable on both architectures from its intended channel.
