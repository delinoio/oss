# Repository Workflow Contract

Repository workflows are reviewed as source-backed contracts. Workflow IDs, job IDs, artifact names, permissions, triggers, protected environments, and publication boundaries must match the checked-in YAML and the static workflow tests.

## Continuous integration

`.github/workflows/CI.yml` is a read-only validation workflow. It uses `contents: read` and `pull-requests: read`, does not consume repository secrets, and must not push tags, create or upload releases, submit stores, push OCI images, deploy documentation or infrastructure, promote updater state, or call any mutating release-controller operation. Release workflows and packaging inputs are tested as source and deterministic fixtures only.

CI never builds a signed private candidate and never publishes.

Each DevHud job performs its own `dorny/paths-filter` gate. The filters cover `servers/**`, `protos/**`, `packages/**`, all three DevHud app workspaces, `crates/devhud-native-messaging-host/**`, `packaging/devhud/**`, public documentation, and the private-package, public-release, and CEF-review workflows. Manual dispatch and changes to `CI.yml` force the applicable checks. Node workspace checks install from the clean frozen lockfile and use the repository's committed Turbo binary with `pnpm exec turbo run <task> --affected --filter <workspace>`; an empty affected set is a successful no-op.

Generated protocol output and package-local frontend output are deterministic cacheable Turbo products. The ignored administrator bundle, native host, desktop installer, mobile, smoke, signing, release, and deployment tasks are explicitly non-cacheable. CI validates schemas and generated freshness; Go formatting, vet, unit, PostgreSQL migration, integration, API, and sweeper behavior; Rust formatting, Clippy, unit, capture, shortcut, IPC, and updater behavior; frontend type, lint, unit, component, accessibility, build, security, and adapter fixtures; exact CEF pins and feasible native architecture builds; extension/native-host/installer packages; SPDX SBOM and provenance; non-root multi-architecture API and migration-bearing sweeper OCI layouts; public routes; and release workflow fixtures.

The `ci-result` job depends on every required CI job and fails if any dependency failed or was cancelled. A self-gated no-op job still completes successfully and remains visible to the aggregate.

Run the repository CI contracts locally from a clean checkout with:

```sh
pnpm install --frozen-lockfile --ignore-scripts
pnpm ci:workflows
pnpm ci:contracts
pnpm ci:release-fixtures
```

Package-local commands are documented in the applicable workspace README. Use `pnpm ci:affected <task> --affected --filter <workspace> --dry=json` to inspect affected execution without downloading a different Turbo version.

DevHud private packaging is manual-only or explicitly called by `release-devhud.yml`; `plan-only` is credential-free and `signed-private` is protected. Its OCI jobs generate and validate the ignored administrator production bundle before host-side Go tests, while the Docker build independently generates that same contracted structure inside its clean build boundary before compiling API and sweeper from one tree. The monthly `devhud-cef-security-review.yml` workflow is manual or scheduled monthly, has read-only contents permission, and may upload only its bounded metadata report artifact. It must not mutate source, pins, lockfiles, releases, stores, registries, deployments, alerts, or updater state.

Changes to DevHud workflows or these contracts must update `docs/apps-devhud-operations-contract.md`, `docs/apps-devhud-support-contract.md`, `docs/project-devhud.md`, the relevant release contract text, `AGENTS.md`, and `scripts/release/devhud-operations.test.mjs` together. `CI.yml` runs the static release/operations contract suite without contacting a controller or publication service.

## Runmoor preview validation and release

`.github/workflows/runmoor.yml` is a separate read-only `contents: read` validation workflow, gated to Runmoor source, release scripts/workflows, and shared Go module changes. It runs race tests and explicitly enabled local Docker integration without GitHub credentials or job assignment. Real Tart integration remains operator opt-in. Existing CI aggregation, application development commands and fixed ports are unchanged.

`.github/workflows/release-runmoor.yml` accepts exact `runmoor@v<MAJOR.MINOR.PATCH>` tags and manual version/dry-run dispatch. Source version and revision must match the release plan; publication permits main or the exact tag only. Plan, build and package jobs use read-only contents authority and produce the three native platform archives plus SHA256SUMS. Dry runs cannot request OIDC, sign, create tags/releases, or upload public assets. Only the explicitly guarded publish job obtains `contents: write` and `id-token: write`, rejects conflicting existing tags or existing public releases, signs the exact archives/checksum file, verifies the exact workflow certificate identity and issuer, and publishes a prerelease with overwrite disabled. No macOS/Xcode images or placeholder signatures are shipped.

Run `node --test scripts/release/runmoor.test.mjs` for deterministic archive, identity, checksum, signature-verifier-double and workflow isolation checks. Cross-build with `node scripts/release/runmoor.mjs build --version 0.1.0 --revision <40-hex-commit> --ref <git-ref> --mode dry-run --output <temporary-directory>`, then its `checksums` and `verify` commands. These commands do not need credentials or signing tools. Repository-wide release fixtures that use Debian packaging and GNU tar run on Linux. Follow `docs/cmds-runmoor-foundation.md` for runtime verification and preview limitations.

## TaskFlow conformance

`taskflow-conformance` and `taskflow-docker` self-gate on TaskFlow, shared Rust, and workflow inputs, participate in `ci-result`, and remain read-only. Native conformance covers macOS/Linux/Windows x64 and arm64, including real native inventories and generated-workflow actionlint. Docker/S3 runs on Linux x64/arm64 with immutable multi-architecture image digests and temporary fixture-only credentials. Existing development/build/test workflows are not migrated to TaskFlow.
