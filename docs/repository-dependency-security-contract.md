# Dependency security contract

## Scope and ownership

Root manifests and lockfiles own dependency resolution. Domain manifests retain their existing runtime, feature, platform, and licensing boundaries. Go's security baseline is `1.26.8` in `go.mod` and the API/sweeper Docker build image; the patched `golang.org/x/crypto` line requires Go 1.26. CI consumes the module's version. Registry dependencies retain their upstream licenses.

Security updates must preserve the immutable DevHud Tauri/CEF revision and mobile target definitions. A compatible patch may change a mobile closure digest only after comparison with the previously verified graph. Unresolved advisories remain visible: missing patches, fixed runtime pins, and limited reachability are not equivalent to a fixed package. Do not add blanket audit exclusions or dismiss alerts merely to make a report green.

## September 25, 2026 remediation

GitHub reported 39 open Dependabot alerts at the start of this change. The changes below remove affected versions for 34 of those alerts. GitHub closes version-based alerts after the updated dependency graph reaches the default branch; opening a PR does not itself close them.

| Dependency | Updated resolution | Coverage |
| --- | --- | --- |
| `js-yaml` | 4.3.2 in root, DevHud and pnpm override | Alerts 223, 224, 235 |
| `body-parser` | 1.20.6 through Express 4.22.3 | Alert 265 |
| `react-router` / `react-router-dom` | 7.18.4 | Alert 205 |
| `pypdf` | 6.16.1 in test-only rendering requirements | Alerts 242–264 |
| `hickory-resolver` / `hickory-proto` | 0.26.3 | Alerts 240, 241; preserve disabled DNSSEC, one attempt, no cache and both IP families; map fallible resolver construction to the existing redacted configuration error |
| Docker SDK | Remove `github.com/docker/docker`; use official Moby client 0.6.0 / API 1.56.0 | Alerts 236–239; preserve local Unix sockets, API negotiation, ownership, resource budgets and cleanup |

The existing Express and YAML overrides are advanced within their current major versions. The new React Router DOM override keeps Rspress on a patched compatible 7.x router pair; remove it when the owning upstream dependency enforces a safe minimum and a fresh lockfile audit remains clean. There is no override to Router 8.

Additional ecosystem audits identified and addressed:

- Go standard-library findings in 1.25.8 through the Go 1.26.8 baseline, plus `golang.org/x/crypto` 0.56.0 and `golang.org/x/mod` 0.40.0.
- `h2` 0.4.19, `rustls` 0.23.45 and `rustls-webpki` 0.103.15 for HTTP/2 and TLS advisories.
- `anyhow` 1.0.103 and `event-listener` 5.4.2 for unsoundness advisories; `serial_test` 4.0.1 removes the vulnerable test-only `scc` 2.x dependency.
- `quick-xml` 0.41.0 for repository-owned Office package and Native Messaging parsers, and test-only `lopdf` 0.42.0 for bounded PDF parsing.
- Replace clibox's unmaintained `im-rc` / `sized-chunks` / `bitmaps` graph with `imbl` 7.0.2. Its `imbl-sized-chunks` 0.2.0 implementation updates initialized ranges before destructor execution, addressing the panic-safety issue rather than merely renaming the dependency. `GenericOrdMap` uses `RcK` to retain non-atomic, single-threaded structural sharing and the existing bounded YAML merge/diff behavior.
- Replace the yanked `chacha20` 0.10.1 resolution with 0.10.2.

All six DevHud mobile graphs were checked against their previous committed digests. Replacing `anyhow` 1.0.102 with 1.0.103 is their only change. The rebaselined hashes do not introduce new packages, features, networking, desktop dependencies, or target definitions.

## Unresolved upstream constraints

The following five Dependabot alerts still match packages in `Cargo.lock`:

| Alerts | Dependency path | Constraint and evidence boundary |
| --- | --- | --- |
| 181 | DevHud → pinned Tauri/GTK3 → `glib` 0.18.5 | `VariantStrIter` is fixed in 0.20, which is incompatible with GTK3's 0.18 type graph. No 0.18.6 patch is published. Repository code and inspected GTK/Tauri consumers do not call `array_iter_str`, but this is source inspection, not a patched dependency or complete Linux binary reachability proof. |
| 182 | Pinned Tauri/kuchikiki → selectors → phf code generation → `rand` 0.7.3 | No 0.7.4 patch is published. The consumer uses seeded `SmallRng`; the all-target/all-feature graph does not enable rand 0.7's `log` feature required by the advisory. The lockfile version still matches the alert. |
| 212–214 | Pinned Tauri CLI/bundler → rpm 0.25.1 → pgp 0.19.0 → `ml-dsa` 0.0.4 | The patched ML-DSA API is incompatible with pgp 0.19; no compatible pgp 0.19.1 or rpm 0.25.2 release is published. DevHud's supported Linux packages are Debian and AppImage, but the optional Linux bundler graph still contains RPM signing code. |

Fixing these dependency versions requires an upstream Tauri/GTK/packaging migration or an explicitly reviewed exception to the repository's pin/patch restrictions. This change adds neither a fork nor a prohibited Cargo patch and does not dismiss these alerts.

Raw Cargo audit also retains the following findings, beyond those five GitHub alerts:

- `quick-xml` 0.30.0 enters through the pinned `xcb` build dependency, which parses upstream build schemas. Version 0.39.4 enters through `pptx` 0.1.0 used for generated presentation parts. Their upstream constraints do not accept 0.41; repository-owned imported-document validation now uses 0.41. These old versions still appear in the raw audit.
- `rsa` 0.9.10 enters through pinned Tauri macOS signing and RPM/PGP packaging. RustSec lists no patched release for the Marvin timing advisory; runtime reachability is not inferred from the packaging-only dependency path.
- Remaining unmaintained-package and yanked-package notices in the pinned/native/font/tooling graph are reported by raw audit, not silently suppressed. In particular, the wasm-bindgen/js-sys pins are not broadly upgraded as part of this focused security update.

Go's verbose audit still lists the deprecated `golang.org/x/crypto/openpgp` package at module scope. It has no fixed version and is not an imported package in the repository's Go graph; the required module is also used for supported cryptography packages. This finding is retained in the audit output.

## Validation

Use GitHub's authenticated Dependabot API together with `pnpm audit`, `cargo audit`, `govulncheck -show verbose ./...`, and `pip-audit -r packages/react-forge/scripts/render-requirements.txt`. Keep scanner databases and Python environments outside the checkout. Raw Cargo audit is expected to remain nonzero for the unresolved constraints above; do not describe it as clean.

Generate the administrator and ach UI embeds before root Go validation, and pnport plus its platform preload before root Rust tests. On macOS, build `pnport` and `fspy_preload_unix` with `fspy_preload_unix/pnport`, then run `cargo test --workspace --exclude fspy --features fspy_preload_unix/pnport` followed by `cargo test -p fspy`: generic fspy requires its default preload mode. Both passes completed successfully. Run the full DevHud frontend suite, mobile/CEF pin checks, public docs tests, root CI contract tests, Runmoor tests/race/vet and opt-in local Docker integration, and React Forge build/lint/tests/examples plus test-only rendering. Root Rust tests on macOS require a canonical temporary directory to avoid pre-existing `/var` versus `/private/var` path fixture mismatches.

The React Forge package suite passes 103 tests with Node 24.19.0, including installed-archive and stdio MCP cases. Node 24.11.0 reproduces existing virtual-inline MCP loader failures; the validated newer 24.x runtime does not require a source change. Updated `pypdf` passes the seven generated/edited Office/PDF cases with XML, text extraction, semantic-tag, raster and source-pagination checks. Historical committed renderer reports retain their original tool versions.

Code scanning returned no analysis, and Secret scanning is disabled on the repository; neither is claimed as a successful security scan.

The full Rust run exposed an existing concurrent-publication fixture assumption: Unix can safely reject a destination handle unlinked by another successful writer. A deterministic zero-link test preserves the production rejection, while the concurrent fixture accepts only that exact failure and still requires a complete successful result and cleaned staging. Production publication behavior is unchanged.
