### Instructions for `packages/`

- Follow root `AGENTS.md` and the owning project/domain contracts.
- Public npm packages generated here declare Apache-2.0 and include the complete license; preserve bundled third-party notices.
- Generated packages must have a canonical source contract, reproducible generation, freshness checks, and no implicit secret or persistence policy.

### Scope in This Domain

- `packages/clibox`: private source workspace generating the public `@delino/clibox` launcher and eight native npm packages.

- `packages/devhud-api-client`: implemented generated TypeScript DevHud API client, Connect Query bindings, and safe handwritten wire helpers.

- `packages/docs-site-switcher`: shared accessible documentation site selector used by the consolidated Public Docs root and all six project content sections. Follow `docs/packages-docs-site-switcher-contract.md`; keep its fixed site registry, enum IDs, keyboard behavior, focus management, and active-route semantics synchronized across consumers.

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

### async-commit-hook
- `packages/async-commit-hook-api-client` owns `@delinoio/async-commit-hook-api-client`, generated exclusively from `async_commit_hook.v1`. Follow `docs/packages-async-commit-hook-api-client-contract.md`; no client-side duplicate gate logic or persistence.

### clibox Rules

- Keep the npm README and `apps/public-docs/docs/clibox` aligned with user-facing command and installation behavior; link to `https://oss.delino.io/clibox`. The shared documentation selector uses clean same-origin paths on production and the consolidated development server, including clibox and pnport as destinations.

- Native and installed npm commands must share the canonical `run env`, `port list`, and `hash compute` names, quiet/PID output separation, stdout dash selector, force validation, and numeric owned-operation cancellation (130/143); validate the migration in installed consumer smoke tests without launcher-side argument rewriting.
- Native and installed npm smoke tests must exercise `system cpus` in default, logical, JSON, and quiet forms across all eight targets. The launcher forwards it unchanged and never computes or substitutes a CPU count.
- Native and installed npm commands must also share the five `run with-*` wrappers for rate admission, lock ownership, service readiness, retries, and timeouts. The launcher remains a literal argv/stdio/signal forwarder: it must not parse wrapper options, own local state, make readiness requests, or introduce a shell.

- Follow `docs/packages-clibox-distribution-contract.md`. Keep the source workspace private, with no unpublished platform dependencies; generate public manifests and exact optional dependencies during packaging only.
- Preserve platform/libc/version checks, literal native argv execution, inherited stdio, and signal/exit propagation. No runtime downloads, install hooks, public JavaScript API, or system binary fallback.
- Windows console Ctrl+C/Break reaches both launcher and native child. Await native cleanup and numeric completion instead of forwarding these events with Node's forceful kill API; preserve explicit Unix signal forwarding. Cover both policies with unit fixtures and isolated Windows console integration.
- Keep tests runnable with Node built-ins, and smoke-test npm/pnpm consumer tarball installs with scripts disabled, including missing-subcommand help on stderr with exit code 2, native JSON readiness, environment execution with literal empty argv/exit propagation and dotenv list/merge plus YAML normalization with reference resolution, literal value preservation, idempotence, and silent file publication. Consumer smoke builds must use release mode so TLS-enabled debug binaries do not exceed bounded archive inspection. Run Rust command/process/adapter/readiness tests on Linux, macOS, and Windows through the existing clibox CI matrix. Build/package/release tasks are package-owned; native integration and publication tasks are not Turbo-cacheable.
- Installed consumer smoke tests must also exercise the seven issue #917 commands as well as help/version across the existing eight target artifacts. Run Rust command/process/adapter tests on Linux, macOS and Windows through the existing clibox CI matrix. Keep command documentation consistent between native and npm distributions without exposing release internals.
- Default consumer smoke builds must use Cargo's stripped release profile within the bounded archive reader; an explicit `--binary` must use the supplied native artifact without rebuilding or changing it.
- Preserve all eight artifacts, native readiness tests, and Alpine consumer checks when TLS dependencies change. musl uses target-native C compilation for statically linked ring and pinned self-contained `rust-lld` for final linking; no dynamic OpenSSL runtime dependency. Keep user-facing wait usage, limits, output, cancellation, and troubleshooting in the npm README.
- Validate complete artifact inventories and source identity before publication. Confirm all native dependencies before the main package and reject conflicting existing integrity. Allow at most 121 registry readback checks with ten-second delays and 30-second request timeouts after each successful upload, log pending confirmations, and never repeat that write during polling.
- Accept LF and CRLF source manifests. Pack launcher text, README, and license as canonical UTF-8/LF and verify their exact canonical bytes across build/assembly operating systems; native executable bytes must never be normalized.
- Require archive execute bits for Unix native binaries and the npm bin shim; Windows PE payloads must remain valid when packed from NTFS without POSIX execute bits. Pin Node's release-build architecture to the selected Rust target.
- Finalize the executable header mode during tarball creation before recording integrity, independent of host filesystem permissions. Artifact verification must never repair or rewrite downloaded tarballs.
- Build both Linux musl targets with the pinned Rust toolchain's `rust-lld` and self-contained runtime objects. Keep native-host and Alpine consumer execution gates; adding C dependencies requires revisiting this toolchain contract.

- clibox GNU npm and GitHub Release archives must contain the same verified AlmaLinux 9/glibc 2.34 binaries. Its separately guarded GitHub publisher validates the complete nine-tarball input, exact tag/commit and source version, preserves immutable assets and reuses verified signatures before stable APT/DNF publication. The npm enable flag gates only npm. Neither publisher may query crates.io or require Cargo registry publication. Include all five clibox crate directories in package test inputs and native CI selection.

### pnport Rules

- Private `packages/pnport` generates @delino/pnport and six exact-version native packages with preferUnplugged. Follow `docs/packages-pnport-distribution-contract.md`; no install hooks, runtime downloads, compilation or unrelated PATH fallback. All six execution/install gates precede publication.
- Package each native executable with the adjacent interception library from the same build. Verify exact inventories, executable mode, source revision, versions, checksums and existing remote integrity before publishing six optional packages ahead of the launcher. Homebrew retries must reject older versions instead of downgrading the tap. Direct installers must search all GitHub Releases pages for the highest stable pnport version. Installer changes must select the six-host native CI matrix on main, and installation smoke must run the public launcher on each host. Keep generated `dist` untracked and remove it from final worktrees.
- pnport native TypeScript conformance pins Yarn and the official compiler in its fixture and lockfile. Keep networked preparation separate from execution, disable Turbo caching for both conformance commands, and record the actual OS/architecture and compiler digest. The corrected official `typescript` package exposes `tsc`; never silently substitute it for an older `native-preview` package's `tsgo` command or rewrite its signature.
- Run the pnport TypeScript conformance suite on glibc Linux as well as macOS. Linux checks the official native compiler's static ELF machine and runs inline/split builds offline; record Docker architecture and tracing limitations without counting emulated amd64 as native x64 release evidence.

### React Forge Rules

- Figma sessions follow `docs/packages-react-forge-figma-contract.md`: local React rendering, explicit remote publication, credential-free receipts, official MCP, shared rate admission and reconciliation before uncertain-write retries. Isolate these exceptions from Office/PDF. CI uses fake MCP and synthetic credentials.

- `packages/react-forge` owns the private `@delino/react-forge` npm package, real React reconciler, format components, one-shot `react-forge` TSX CLI and local session-based stdio MCP server. Follow `docs/packages-react-forge-mcp-contract.md` for MCP execution, retained state and transport isolation. Package imports and workspace task filters use the scoped npm name; the directory, project ID and executable keep `react-forge`. Follow `docs/packages-react-forge-contract.md` and all requirements from #968. Pin React 19.2.8 and react-reconciler 0.33.0; support Node.js 24 on macOS, Windows and glibc Linux, each with x64 and arm64. Enforce the native host restriction in build/loading code, not workspace manifest `os`/`cpu` fields: pnpm emits platform warnings on unrelated commands and corrupts binary protocol stdout.

- Sessions are memory-only. Serialize mutations, reject invalid latest renders and overlapping targets, pin export revisions, serialize file exports sharing a directory identity in invocation order across sessions, support cancellation without timeouts, and clean resources on dispose without deleting exports. Keep callbacks and component code outside native computations. Reject exports to an imported document's tracked source or canonical aliases even with explicit overwrite; require a separate output path because fingerprint-then-rename cannot protect external saves.
- Imported-source protection must conservatively reject case aliases on Windows/macOS and Unicode normalization aliases on macOS, including after the original source has been removed.
- Internal docs, examples, messages and troubleshooting are English; no public publication or runtime downloads. Tests must exercise installed/workspace CLI and asynchronous React behavior, not manually invoked components.

- React Forge native/system-font build, integration, rendering and benchmark tasks must remain uncached. Use package-owned scripts and record test renderer/font provenance without bundling system fonts. Retain actual packed-consumer CLI coverage. Fonts/assets participate in immutable revision snapshots; disposal owns in-flight mounts and mutations.
- Keep the ROAM travel IR example's reusable React source, local image assets, generation prompts and provenance together. Resolve its assets relative to the task module, retain explicit fictional-metric labels and calculation consistency, and keep exported decks/previews untracked. The example must run without network access or image-generation/conversion tools and must generate successfully on all six native CI hosts with their installed fonts.

- Word list components must carry stable list-instance identity into the native model. Separate numbered lists restart independently; items within a list share numbering, and imported numbering definitions remain untouched.
