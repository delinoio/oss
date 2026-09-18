# Repository Workflow Contract

Repository workflows are reviewed as source-backed contracts. Workflow IDs, job IDs, artifact names, permissions, triggers, protected environments, and publication boundaries must match the checked-in YAML and the static workflow tests.

## Continuous integration

`.github/workflows/CI.yml` is a read-only validation workflow. It uses `contents: read` and `pull-requests: read`, does not consume repository secrets, and must not push tags, create or upload releases, submit stores, push OCI images, deploy documentation or infrastructure, promote updater state, or call any mutating release-controller operation. Release workflows and packaging inputs are tested as source and deterministic fixtures only.

CI never builds a signed private candidate and never publishes.

Each DevHud job performs its own `dorny/paths-filter` gate. The filters cover `servers/**`, `protos/**`, `packages/**`, all three DevHud app workspaces, `crates/devhud-native-messaging-host/**`, `packaging/devhud/**`, public documentation, and the private-package, public-release, and CEF-review workflows. Manual dispatch and changes to `CI.yml` force the applicable checks. Node workspace checks install from the frozen lockfile and use `pnpm exec vp run <package>#<task>` with the exact Vite+ version committed in the manifest and lockfile. Each job uses its path filter to select execution; selected jobs run every designated check without a second affected-package calculation. Unselected jobs complete as successful no-ops. Task configurations, shared orchestration scripts, workspace dependencies, and external build inputs participate in the filters.

Generated protocol output and package-local frontend output are deterministic cacheable Vite Task products. The ignored administrator bundle, native host, desktop installer, mobile, smoke, signing, release, and deployment tasks are explicitly non-cacheable. CI validates schemas and generated freshness; Go formatting, vet, unit, PostgreSQL migration, integration, API, and sweeper behavior; Rust formatting, Clippy, unit, capture, shortcut, IPC, and updater behavior; frontend type, lint, unit, component, accessibility, build, security, and adapter fixtures; exact CEF pins and feasible native architecture builds; extension/native-host/installer packages; SPDX SBOM and provenance; non-root multi-architecture API and migration-bearing sweeper OCI layouts; public routes; and release workflow fixtures.

The `ci-result` job depends on every required CI job and fails if any dependency failed or was cancelled. A self-gated no-op job still completes successfully and remains visible to the aggregate.

Run the repository CI contracts locally from a clean checkout with:

```sh
pnpm install --frozen-lockfile --ignore-scripts
pnpm exec vp run ci:workflows
pnpm exec vp run ci:contracts
pnpm exec vp run ci:release-fixtures
```

Package-local commands are documented in the applicable workspace README. Use `pnpm exec vp run <package>#<task>` for targeted execution and `pnpm exec vp run -v <package>#<task>` for execution details. The repository task contract defines caching, dependencies, lifecycle ownership, and the command migration.

DevHud private packaging is manual-only or explicitly called by `release-devhud.yml`; `plan-only` is credential-free and `signed-private` is protected. Its OCI jobs generate and validate the ignored administrator production bundle before host-side Go tests, while the Docker build independently generates that same contracted structure inside its clean build boundary before compiling API and sweeper from one tree. The monthly `devhud-cef-security-review.yml` workflow is manual or scheduled monthly, has read-only contents permission, and may upload only its bounded metadata report artifact. It must not mutate source, pins, lockfiles, releases, stores, registries, deployments, alerts, or updater state.

Changes to DevHud workflows or these contracts must update `docs/apps-devhud-operations-contract.md`, `docs/apps-devhud-support-contract.md`, `docs/project-devhud.md`, the relevant release contract text, `AGENTS.md`, and `scripts/release/devhud-operations.test.mjs` together. `CI.yml` runs the static release/operations contract suite without contacting a controller or publication service.
