# bundlewhy adapters and distribution contract

## Scope

`packages/bundlewhy` tests the engine collector adapters, which live in `crates/bundlewhy/adapters` (`common.mjs`, `vite.mjs`, `rspack.mjs`) and are embedded by the binary. It owns the npm/npx launcher (`bin/bundlewhy.cjs`), acceptance fixtures (`fixtures/vite-app`, `fixtures/rspack-app`) and acceptance scripts.

## Runtime and Language

Adapters are dependency-free ESM for Node.js 22.18 or newer. They run in the analyzed project's directory and load the project's own `vite` or `@rspack/core`. The launcher is CommonJS using Node.js built-ins.

## Users and Operators

Developers running `npx @delino/bundlewhy analyze ...` and maintainers running acceptance.

## Interfaces and Contracts

- Adapter protocol: `node <adapter>.mjs <request.json>`. The request carries the project root, selected configuration, configuration name, build directory, collection path and the frozen supported version. Adapters write protocol frames to stdout, one per line: `BUNDLEWHY-FRAME <protocolToken> <json>`, where the per-run token comes from the request and the JSON is a `progress`, `engine`, `error` (with a `BW_*` code and recovery) or `done` event. A `protection` event requires a token-matched acknowledgement on stdin after the Rust supervisor hashes the selected root files; the adapter waits before loading user configuration or starting Vite. Lines without the exact prefix and token are project output and are ignored, so JSON printed by configuration or plugins cannot impersonate adapter events. Adapters also write and one collection file (`collectVersion: 1`) with engine identity, overrides, a safe configuration summary, normalized modules (identifiers, paths, importers, exports, used exports, input format, rendered size, chunk files, bailouts, reasons, nearest package identity, hook-level and configured `moduleSideEffects` values for Vite), chunks, assets, warnings and structured engine logs. The Vite adapter (version `5`) records, for every chunk, the optional `imports`, `externalImports` and `dynamicImports` lists of the finished build's bundle and keeps them only when the finished build's chunk files and per-chunk module membership match the module snapshot and no chunk's generated code changed after it, otherwise it omits the three lists of every chunk (the Rust crate contract defines the exact rule and how the analysis treats a chunk without them). Version `3` added the optional `mangle` field (`enabled` or `disabled`) and the matching `build.rolldownOptions.output.minify.mangle` configuration summary entry. It resolves the value from the resolved Vite configuration and then replaces it in `renderStart` with the finalized output options of the first output, so a plugin `outputOptions` hook that changes `output.minify` is respected; only a verified disabled setting (`false` while Rolldown owns minification, `'dce-only'`, or an options object without `mangle`) writes `disabled`. A collection without the field, including every Rspack collection, is read as `enabled`, and the Rust crate contract defines how `disabled` skips the direct-eval detector and makes the Name mangling category not applicable. The Rspack adapter (version `2`) adds the optional `exportUsage` record (edges, availability, reason and skipped-edge count) collected through an appended observation-only `RsdoctorPlugin`; it records that plugin as a collection override, never replaces the project's engine or plugins, guards construction, `apply`, every hook and the payload, repeats a failed build once without the plugin in a new adapter process that loads the configuration and its imports from scratch, and reports `available: false` with a fixed reason code instead of failing the build when the API, hook or graph is unavailable. The reason is one of the stable codes in the Rust crate contract; the adapter never copies error text, source content, project paths or credentials into it or into its logs. Shared decoding lives in `adapters/common.mjs` and is covered by the adapter tests with a wrapper around the real `@rspack/core` that removes or reshapes the plugin API.
- Adapters never write project source, configuration or lockfiles; build output goes to the bundlewhy run directory. Vite keeps a configured relative or absolute root for the diagnostic build and collection context; when no root is configured, it uses the selected project root. Configuration summaries contain option names and summarized values, never environment values.
- The Rust binary embeds the adapter files and writes them into each run directory, so the installed adapter version always matches the binary.
- Distribution: the public `@delino/bundlewhy` launcher resolves a platform package for macOS arm64 (`darwin-arm64`), Linux x64 with glibc (`linux-x64-gnu`) and Windows x64 (`win32-x64-msvc`) and executes its binary without a shell, forwarding arguments, stdio, exit codes and signals. Unsupported platforms and missing platform packages report a structured error with reinstall guidance; there is no download or compile fallback. Release artifacts publish exact Node.js and platform requirements and the frozen engine matrix. Rollback installs a previous version.
- The source workspace stays private. Release tooling explicitly generates the public launcher and three platform manifests; it never changes the source package to public or adds unpublished platform dependencies to the workspace.

### Release workflow

`Release Project` includes `bundlewhy`. It synchronizes `crates/bundlewhy/Cargo.toml`, the workspace entry in `Cargo.lock` and `packages/bundlewhy/package.json` in one version-only commit, validates the immutable source and pushes `bundlewhy@v<version>`. The crate retains `publish = false`; this path needs no Cargo registry token and does not publish to crates.io. The coordinator does not wait for the downstream distribution workflow.

`release-bundlewhy.yml` accepts the exact version tag or a manual dry run (default `dry_run=true`). Publication requires the first-party exact tag and commit; a manual publication must select that tag, while a development branch may only dry-run. A project-wide concurrency group serializes versions without cancelling an active release.

The native release matrix contains only macOS arm64, Linux x64/glibc and Windows x64/MSVC. Each host installs the frozen workspace with scripts disabled, tests and builds the self-contained web UI before Cargo compilation, runs the package/adapter tests and native Rust tests, builds a release binary and verifies its version. Linux uses the shared digest-pinned AlmaLinux 9 image with baseline x86-64 and glibc 2.34. Release builds must never set `BUNDLEWHY_ALLOW_MISSING_UI`. Native npm and pnpm consumer smoke tests install the candidate with scripts disabled and exercise help, version, engine matrix, usage errors, both fixture analyses, and interactive HTML export from the installed launcher. A missing platform dependency must fail with reinstall guidance. Generated `dist` output is removed after native validation, including failures.

The source package owns `scripts/common.mjs`, `package.mjs`, `smoke.mjs`, `publish.mjs` and `github-release.mjs`. The package builder reuses clibox's bounded tar reader and executable-mode finalizer. The publishers reuse clibox's registry integrity reconciliation and immutable GitHub draft publication, plus the shared source-bound Sigstore verifier. These consumers are explicit CI/Turbo inputs; native consumer tasks are not cacheable.

Generated packages carry the exact source commit, version, platform metadata, README, complete root Apache-2.0 LICENSE and NOTICE. The launcher pins all three optional native dependencies to its exact version and requires Node.js 22.18+. Only the launcher, notices and native executable belong in the generated packages; no install hook or project dependency is shipped. Text is canonical UTF-8/LF across Windows and Unix builds, and Unix executable modes are finalized before integrity is recorded. Assembly rejects missing or extra packages, source/version drift, changed launcher text and notices, and malformed archives before publication.

Native jobs upload `bundlewhy-native-<suffix>` artifacts. Assembly verifies exactly four npm tarballs and retains `bundlewhy-npm-<version>-<revision>` for 30 days. Both publication jobs consume that exact verified set. npm publishes and confirms the native packages before publishing the launcher, using npm 11.6.2, GitHub Actions OIDC and provenance. Each of these packages requires npm Trusted Publisher configuration for repository `delinoio/oss` and workflow `release-bundlewhy.yml`:

- `@delino/bundlewhy`
- `@delino/bundlewhy-darwin-arm64`
- `@delino/bundlewhy-linux-x64-gnu`
- `@delino/bundlewhy-win32-x64-msvc`

The signed GitHub Release publishes those same four `.tgz` files, SHA256SUMS and a source/workflow-bound Sigstore bundle for each file (ten assets total). Each native tarball contains its executable at `package/bin/bundlewhy` or `package/bin/bundlewhy.exe`; it is also the npm platform payload. GitHub and npm publication are separate downstream jobs, and either can be recovered independently. Before any write, existing versions/assets must match the verified bytes; conflicting contents fail. The GitHub publisher discovers the unique draft, resumes missing uploads, verifies remote contents and signatures, then publishes. A complete public release is read-only. Rerun the failed publication job with the retained artifact; never move a tag or replace public assets to recover a partial release. A dry run validates packaging without requesting publication credentials or signing.

Public availability notices remain until actual publication is verified. Fixture, package and local native checks do not establish hosted three-platform or external-application acceptance.

## Storage

None beyond the run directory chosen by the user.

## Security

Adapters execute project configuration and plugins with the user's permissions. They do not read environment values into collections or logs.

## Logging

Adapters report progress and errors as JSON lines; Rust records them in the run log without source content.

## Build and Test

`pnpm --filter @delino/bundlewhy test` runs launcher and adapter tests, including the adapter unit tests that live next to the adapters (`crates/bundlewhy/adapters/*.test.mjs`). `pnpm --filter @delino/bundlewhy test:acceptance` builds the Rust binary, runs both fixtures end to end and verifies that fixture files are unchanged. Acceptance removes its generated web `dist` output after success or failure while preserving the original failure diagnostic. Fixtures pin `vite` `8.3.4` and `@rspack/core` `2.2.9` through this package's development dependencies.

## Dependencies and Integrations

- [Rust foundation](crates-bundlewhy-foundation.md)

## Change Triggers

Adapter protocol or collection changes update the Rust collection types and this contract together. Engine version changes update the matrix in the Rust contract and public docs.

## References

- [Project index](project-bundlewhy.md)
- [Repository defaults](repository-defaults.md)
