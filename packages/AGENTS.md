### Instructions for `packages/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Generated packages must have a canonical source contract, reproducible generation, freshness checks, and no implicit secret or persistence policy.

### Scope in This Domain

- `packages/clibox`: private source workspace generating the public `@delino/clibox` launcher and eight native npm packages.

- `packages/devhud-api-client`: implemented generated TypeScript DevHud API client, Connect Query bindings, and safe handwritten wire helpers.

### DevHud Rules

- Keep generated bootstrap types aligned with the `desktop`/`ios`/`android`/`admin` Logto client keys, native callback, and exact admin redirect defined by the protocol and server contracts.

- Generate only from `protos/devhud/v1`; use `@connectrpc/connect-query` and preserve package `devhud.v1`, stable enums, typed errors, and revision conflicts.
- Generated clients must expose the explicit AdminService RPC names and `AccountService.RestoreAccount` defined by the protocol contract, including upload-finalization validation fields.
- Generated administrative and user-upload-list clients must preserve the shared bounded page-size, opaque-token, deterministic-order pagination contract defined by the protocol, including query/user scope.
- Keep `docs/packages-devhud-api-client-contract.md` and `docs/project-devhud.md` synchronized with schema, transport, or generated API changes.
- Generated upload clients must preserve submission-scoped groups, expected 32-byte raw checksum/version fields, immutable finalization semantics, and correlation-ID response metadata.
- Keep committed output under `src/gen` tool-owned and reproducible. Public exports use per-service namespaces such as `UploadQuery` and `AdminQuery` so same-named RPCs do not collide; do not handwrite generated messages or service descriptors.
- Handwritten helpers may validate canonical UUID v7 values, checksums, bounded pagination, RFC 8785 settings bytes without a UTF-8 BOM, bounded NUL-free sensitive-content-safe administrator reasons, required crash client/related correlations, duration, browser-only unknown architecture, explicit browser and native platform revision rules, NUL-free 256-byte crash identifiers, redacted crash details, and typed Connect errors, but must not add persistence, implicit authentication, or another business transport.
- The API client must retain package-local typecheck, lint, unit, and build commands for affected CI execution. Its generated output is cacheable only when derived from the committed schemas and lockfile.

### clibox Rules

- Follow `docs/packages-clibox-distribution-contract.md`. Keep the source workspace private, with no unpublished platform dependencies; generate public manifests and exact optional dependencies during packaging only.
- Preserve platform/libc/version checks, literal native argv execution, inherited stdio, and signal/exit propagation. No runtime downloads, install hooks, public JavaScript API, or system binary fallback.
- Keep distribution tests runnable with Node built-ins, and smoke-test npm/pnpm consumer tarball installs with scripts disabled, including missing-subcommand help on stderr with exit code 2, native JSON readiness, environment execution, literal empty argv, and delegated exit propagation. Consumer smoke builds must use release mode so TLS-enabled debug binaries do not exceed bounded archive inspection. CI additionally runs Rust unit/process tests on Linux, macOS, and Windows. Build/package/release tasks are package-owned; native integration and publication tasks are not Turbo-cacheable.
- Preserve all eight artifacts, native readiness tests, and Alpine consumer checks when TLS dependencies change. musl uses target-native C compilation for statically linked ring and pinned self-contained `rust-lld` for final linking; no dynamic OpenSSL runtime dependency. Keep user-facing wait usage, limits, output, cancellation, and troubleshooting in the npm README.
- Validate complete artifact inventories and source identity before publication. Confirm all native dependencies before the main package and reject conflicting existing integrity.
- Accept LF and CRLF source manifests. Pack launcher text, README, and license as canonical UTF-8/LF and verify their exact canonical bytes across build/assembly operating systems; native executable bytes must never be normalized.
- Require archive execute bits for Unix native binaries and the npm bin shim; Windows PE payloads must remain valid when packed from NTFS without POSIX execute bits. Pin Node's release-build architecture to the selected Rust target.
- Finalize the executable header mode during tarball creation before recording integrity, independent of host filesystem permissions. Artifact verification must never repair or rewrite downloaded tarballs.
- Build both Linux musl targets with the pinned Rust toolchain's `rust-lld` and self-contained runtime objects. Keep native-host and Alpine consumer execution gates; adding C dependencies requires revisiting this toolchain contract.

- clibox GNU npm and GitHub Release archives must contain the same verified AlmaLinux 9/glibc 2.34 binaries. Its separately guarded GitHub publisher validates the complete nine-tarball input, exact tag/commit and crates.io version, preserves immutable assets and reuses verified signatures before stable APT/DNF publication. The npm enable flag gates only npm.
