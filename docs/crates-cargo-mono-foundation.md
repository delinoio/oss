# crates-cargo-mono-foundation

## Scope
- Project/component: `cargo-mono` crate foundation contract
- Canonical path: `crates/cargo-mono`

## Runtime and Language
- Runtime: Rust CLI (`cargo` subcommand integration)
- Primary language: Rust

## Users and Operators
- Release engineers operating monorepo version and publish workflows
- Maintainers running local and CI release automation

## Interfaces and Contracts
- Binary naming contract: `cargo-mono` must remain compatible with `cargo mono` invocation.
- Binary entrypoint must force-link `swc_malloc` allocator policy, while the library target remains allocator-agnostic for downstream consumers.
- Command identifiers for lifecycle operations must remain stable and documented.
- Publish and bump workflows must preserve scriptable output contracts for automation.
- `bump` must not create Git tags.
- `publish` is the only command allowed to create release tags, and only for packages listed in `[workspace.metadata.cargo-mono.publish.tag].packages`.
- Publish tag creation is opt-in by default (no config means no tags), must remain local-only (`git tag` without push), and must use `<crate>@v<version>` naming.
- Remote tag publication is owned by CI automation: `.github/workflows/auto-publish.yml` must run `git push --tags` after a successful `publish` command, with checkout credential persistence disabled and authentication bound to `secrets.GH_TOKEN` (non-`GITHUB_TOKEN`) so downstream tag-triggered workflows run.
- If `publish` tag configuration references unknown workspace packages, command execution must fail with `invalid-input`.
- Human-output color contract:
  - Global CLI flag: `--color <auto|always|never>`.
  - Environment override: `CARGO_MONO_OUTPUT_COLOR=auto|always|never`.
  - Global opt-out: `NO_COLOR`.
  - Precedence: `--color` > `CARGO_MONO_OUTPUT_COLOR` > `NO_COLOR` > auto-detection.
- Machine-readable contract: JSON output must remain ANSI-free and schema-stable regardless of color settings.

## Storage
- Uses workspace metadata and package manifests as canonical input.
- Uses temporary local files/caches only for transient command execution.

## Security
- Publishing workflows must rely on explicit credentials and least-privilege secrets.
- Logs and errors must avoid exposing secret registry tokens.

## Logging
- Use structured `tracing` logs for release automation operations.
- Include command phase, package target, and outcome status for debugging.
- Keep `CARGO_MONO_LOG_COLOR` semantics scoped to structured log rendering (separate from human result-output color controls).

## Error UX Contract
- Runtime errors must use a fixed three-line format:
  - `Summary: ...`
  - `Context: key=value, ...`
  - `Hint: ...`
- Context values must include safe operational data (for example package name, manifest path, command, status, attempt count) needed for debugging.
- Context values must normalize whitespace and be length-limited to avoid noisy or unsafe output.
- Dependency-cycle conflicts from package ordering must include `selected_count`, `selected_sample`, `unresolved_count`, `unresolved_sample`, `cycle_package_count`, `cycle_packages`, and `dependency_scope=all-cargo-metadata-kinds`.
- Cargo metadata load failures must include `working_directory` and `metadata_command` context keys in addition to the underlying `error` details.
- Human stderr must include stable error kind labels while preserving the existing exit-code mapping contract.
- Publish failure summaries must prefer actionable `details_excerpt` content in this order: first `error:` line, then first `failed to ...` line, then first non-empty line.
- Error messaging improvements must not change CLI command behavior or JSON output schema keys.
- Error and log output must not expose secret credentials or registry tokens.

## Build and Test
- Local validation: `cargo test -p cargo-mono`
- Workspace validation baseline: `cargo test --workspace --all-targets`
- CI alignment: `.github/workflows/CI.yml` Rust jobs
- Release contract checks should align with `.github/workflows/release-cargo-mono.yml`.

## Dependencies and Integrations
- Integrates with Cargo workspace metadata and release workflows.
- Integrates with root automation (`auto-publish`) through stable command contracts, including CI-driven tag publication.
- Integrates with tag-based binary distribution automation (`release-cargo-mono`) through stable artifact naming and signing contracts.

## Change Triggers
- Update `docs/project-cargo-mono.md` with this file when command identifiers or ownership changes.
- Update `crates/AGENTS.md` and root `AGENTS.md` when policy or path contracts change.

## References
- `docs/project-cargo-mono.md`
- `docs/domain-template.md`
