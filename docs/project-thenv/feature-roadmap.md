# Feature: roadmap

## Roadmap
- Phase 1: Connect RPC foundation, versioned multi-file bundles, RBAC, and secure push/pull/list/rotate flows.
- Phase 2: OIDC/JWT verification, richer audit filtering/export, and operational hardening.
- Phase 3: External KMS integration, key rotation automation, and retention controls.
- Phase 4: Enterprise governance features (compliance controls, delegated administration, policy automation).


## Open Questions
- OIDC provider and tenant-mapping strategy for production identity.
- KMS backend selection and key lifecycle SLOs for production deployments.
- Maximum payload size and rate-limiting defaults for push/pull APIs.
- Fine-grained audit read permissions for non-admin roles.


## References
- `docs/project-template.md`
- `AGENTS.md`
- `apps/AGENTS.md`
- `cmds/AGENTS.md`
- `servers/AGENTS.md`
- `docs/project-devkit/README.md`
