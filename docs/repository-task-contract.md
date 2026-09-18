# Repository Task Contract

Vite Task, supplied by exact `vite-plus@0.3.3`, owns repository and workspace task orchestration. The manifest and frozen pnpm lockfile pin the local binary. Contributors install with `pnpm install`; a global Vite+ installation is unnecessary. Node.js follows `.nvmrc` and must satisfy the pinned package's engine requirement (24.11 or newer on the repository's Node 24 line).

## Commands and ownership

Runnable tasks live under `run.tasks` in root and workspace `vite.config.ts` files. These configurations contain orchestration only: Rsbuild/Rspress, Vitest/Jest, TypeScript, Go, Cargo, and native packaging remain the implementing tools. `package.json` retains installation lifecycle scripts, including the existing Lefthook and app preparation flow. A task and script must never share a name. Vitest tasks resolve their package-owned CLI explicitly, and disable Node 24 experimental web storage so jsdom owns browser storage. Test configuration stays separate from task configuration. There is no automatic Vite+ migration, Vite/Vitest alias replacement, or global CLI requirement.

| Operation | Command |
| --- | --- |
| Build all workspaces | `pnpm exec vp run build` |
| Test all workspaces | `pnpm exec vp run test` |
| Check one package | `pnpm exec vp run devhud#test` |
| Team development | `pnpm exec vp run dev` |
| OSS development / stop dependencies | `pnpm exec vp run dev:oss` / `pnpm exec vp run dev:oss:down` |
| Public / Nodeup / binpm documentation | `pnpm exec vp run dev:public-docs` / `pnpm exec vp run dev:nodeup-docs` / `pnpm exec vp run dev:binpm-docs` |
| Build embedded administrator assets | `pnpm exec vp run devhud-admin#build:embedded` |
| Protocol validation | `pnpm exec vp run proto:check` |
| Runner / environment fixtures | `pnpm exec vp run test:tasks` / `pnpm exec vp run test:dev-environment` |

Use `vp run` inside task commands and `pnpm exec vp run` from shell scripts, native hooks, Docker, CI, and documentation. Options such as `--no-cache`, `--filter`, `-r`, and `-v` precede the task name; following arguments belong to the task. Multiple checks use separate calls or an explicit aggregate task, not multiple positional task names. Recursive root aggregates rely on the runner's self-reference pruning. Package task names remain stable; the former `pnpm <task>` script aliases and `ci:affected` interface are removed. The old root `format` alias had no configured pipeline or package tasks and is removed without introducing a formatter.

## Dependencies and caching

Every configured task explicitly chooses its cache policy, defaulting to `cache: false`; CI rejects unspecified policies. In 0.3.3 the root `run.cache.tasks` switch must remain enabled for individual opt-ins to work. Only deterministic protocol generation, package-local frontend builds, and selected pure checks are cacheable. Inputs cover the command's files, root manifests, lockfile, workspace configuration, runtime selector, task configuration, and external inputs. Builds use explicit source inputs because automatic directory probes can invalidate a multi-stage build when its outputs are removed. Extension builds include the DevHud icon and public release identity; binpm documentation includes its installer scripts. Cache outputs are bounded to the owning generated directories. There is no remote cache integration.

The generated API client builds before DevHud and administrator consumers. Administrator production generation and validation remain prerequisites for API/sweeper compilation. Docker installs the local runner and its configuration within the administrator stage and produces the same ignored bundle inside its own build boundary.

Development, administrator embedding, native and mobile builds, smoke, signing, release, and deployment remain uncached. Protocol freshness always regenerates with `--no-cache` before checking tracked and untracked generated drift. Local cache storage belongs to ignored `node_modules/.vite/task-cache`; repository-owned `dist` directories remain ignored and must be removed from the final worktree.

## Environment and lifecycle

The repository environment contract remains authoritative. Vite Task 0.3.3 filters environments only when caching is enabled; `cache: false` inherits its launching environment. Therefore the root development wrapper sanitizes before launching Vite Task, and each service wrapper independently validates and sanitizes its owned configuration. Never enable caching or broad environment passthrough to implement development isolation.

The root launches exactly the frontend, administrator, and API development tasks concurrently after preflight and migration. Fixed ports, exclusive checkout ownership, issuer comparison pins, OSS dependency ownership, signal forwarding, process-tree reaping, and volume-preserving cleanup retain their existing behavior. No service credential is passed through task configuration, root arguments, logs, or cache metadata.

## CI and validation

CI selects jobs through `dorny/paths-filter`, including shared dependencies, runner configuration, scripts, and external build inputs. Selected jobs execute all designated checks; no affected-package selector or runner JSON dry-run is used. Manual dispatch and CI workflow changes force applicable jobs. Job IDs, successful skipped-work no-ops, the final aggregate, and read-only publication boundaries remain stable.

The runner fixtures execute the pinned binary in temporary workspaces to prove dependency ordering, failure propagation, cache reuse/restoration, file and environment invalidation, explicit cache bypass, three-service concurrency, sanitized environment delivery, and process-tree termination. They run alongside the environment suite on Linux, macOS, and Windows. Existing frontend, protocol, CI, release, Go, native-pin, and mobile contracts remain required at their respective boundaries.
