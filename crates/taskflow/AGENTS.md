# TaskFlow

- Follow `docs/project-taskflow.md` and `docs/crates-taskflow-foundation.md`.
- Keep the library independently testable; CLI commands use the same planner and executor as CI and sessions.
- Never replace native dependency resolution with package-name matching or generate compiler actions.
- Unknown graph coverage expands affected selection conservatively, but unresolved prerequisite selection fails closed.
- Never collapse direct/input/schedule causes into a prerequisite cause. Cancellation and invalidation prohibit cache publication.
- Output restoration must validate the complete entry and containment before mutation. Secrets must be masked before persistence.
- Every child belongs to a process-tree/container owner; replacement waits for reaping. Tests must assert actual cleanup.
- Watch invalidation consumes filesystem mutations, never access notifications from metadata discovery or input hashing.
- Canonicalize watcher roots and event paths before graph matching, including deleted paths through their existing ancestors and Windows path prefixes.
- Maintain schema freshness and numbered issue #898 conformance scenarios. Report unavailable platform/service evidence accurately.
- Keep `docs/crates-taskflow-conformance.md`, the native/Docker CI matrices, and their centralized `scripts/ci/job-paths.json` ownership synchronized. CI result bundles must prove complete task, artifact, and shard accounting against the exact plan; secret transport is forbidden for PR jobs.
- Probe tools inside the selected Docker image. Remote publication stages content before rechecking cancellation/input state, and session failures await all child owners before returning.
- Existing repository workflows are not migrated and the crate remains unpublished.
