# Feature: roadmap

## Roadmap
- Phase 1: Parser/type-checker/go-emitter/metadata-cache baseline in `cmds/ttlc`.
- Phase 2: Incremental scheduler and runtime task execution with real cache reuse.
- Phase 3: Performance tuning, advanced observability, and richer cache lifecycle controls.


## Open Questions
- Should v2 include distributed cache protocol compatibility?
- Should remote execution be introduced before multi-target backend support?
- Which garbage-collection policy should prune stale cache blobs safely?


## References
- `AGENTS.md`
- `cmds/AGENTS.md`
- `docs/project-template.md`
- [turbo-tasks/lib.rs](https://github.com/vercel/next.js/blob/canary/turbopack/crates/turbo-tasks/src/lib.rs)
- [turbo-tasks/vc/mod.rs](https://github.com/vercel/next.js/blob/canary/turbopack/crates/turbo-tasks/src/vc/mod.rs)
- [turbo-tasks/value.rs](https://github.com/vercel/next.js/blob/canary/turbopack/crates/turbo-tasks/src/value.rs)
