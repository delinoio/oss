# CI task ownership

- `@delinoio/ci` owns repository-wide validation; app/package leaves stay with their workspace. Actions owns setup, affected planning, matrices, temporary services, artifacts and `CI Result`.
- Follow `docs/repository-workflow-contract.md`. Route PR checks through `run-affected.mjs`; preserve exact comparison SHAs, external forcing, full native suites, Windows Go shards and 20/45-minute package watchdogs.
- Cache deterministic leaves only. Go/Rust execution, DB/OS/render/benchmark, repeated clean builds, embedded generation and protocol freshness run every time. Declare actual outputs and external inputs, plus task-local platform/tool/option hashes.
- Vercel OIDC tokens are short-lived and cache-only. Main writes, first-party PR/manual non-main reads, forks skip auth; failures fall back locally and must not suppress validation failures. Keep development's exact environment boundary unchanged.
- Test cold/warm restoration, invalidation and uncached failure propagation in disposable fixtures. Record hosted main-write/PR-hit and comparable timing in PRs or CI artifacts, not repository evidence files.
