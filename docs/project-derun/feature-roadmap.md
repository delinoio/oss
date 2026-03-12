# Feature: roadmap

## Roadmap
- Phase 1: Terminal-fidelity `run` execution and transcript persistence.
- Phase 2: MCP replay/live-tail tool surface and cursor consistency guarantees.
- Phase 3: Cross-platform hardening for PTY/ConPTY behavior and stress tests.
- Phase 4: Optional policy and ACL extensions for session access governance.


## Open Questions
- Final MCP schema versioning strategy and backward compatibility policy.
- Optional compression policy for large session outputs while preserving raw replay fidelity.
- Slow-filesystem lock behavior and retry policy tuning beyond advisory lock v1 baseline.


## References
- `docs/project-template.md`
- `AGENTS.md`
- `cmds/AGENTS.md`
