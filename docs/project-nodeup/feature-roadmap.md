# Feature: roadmap

## Roadmap
- Phase 1: Rustup-style command skeleton (`toolchain`, `default`, `show`, `override`, `run`, `which`).
- Phase 2: Runtime installer, checksum verification, and command-level auto-install behavior.
- Phase 3: Self-management flows (`self`) implemented; completion generation (`completions`) remains pending.
- Phase 4: Cross-platform shim parity and CI hardening.


## Open Questions
- Signature verification scope beyond `SHA256` checksum matching (for example GPG signature validation).
- Cross-platform archive support expansion timeline (Windows zip installation path).
- Self-update rollout policy and release channel strategy for `nodeup` binary updates.


## References
- `docs/project-template.md`
- `AGENTS.md`
- `crates/AGENTS.md`
- `crates/nodeup/README.md`
