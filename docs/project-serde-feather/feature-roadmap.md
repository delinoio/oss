# Feature: roadmap

## Roadmap
- Phase 1: Document-first skeleton with workspace wiring and minimal feature contracts. (completed)
- Phase 2: Derive macro API design and stabilization plan. (completed)
- Phase 3: MVP derive implementation for named structs with compatibility tests. (completed)
- Phase 4: Expand type and attribute coverage (tuple/unit structs, enum tuple/named variants, generic bounds, `rename_all`/`alias`/`with`/`skip_serializing_if`). (completed)
- Phase 5: Evaluate publish readiness and lift `publish = false` when contracts are stable.


## Open Questions
- Whether no-std + alloc derive support should be introduced after std-first behavior.
- Whether `rename_all_fields`, tagging (`tag`/`content`), or `flatten` should be supported in a future phase.
- Whether build-time optimization of generated visitors should be prioritized before publish readiness.


## References
- `docs/project-template.md`
- `AGENTS.md`
- `crates/AGENTS.md`
