# Documentation Lifecycle

This page defines how documentation changes should be coordinated between internal contracts and public docs.

## Source Layers

- Internal contracts: Source of truth for project contracts and implementation details.
- This public site: Public-facing documentation surface for curated guidance.
- Standalone product sites: Public-facing documentation surfaces for product-specific user guidance.

Internal contracts may include repository architecture, repo-local paths, implementation details, and maintainer operations. Public documentation must translate those contracts into user-facing behavior, supported workflows, stable public interfaces, and explicitly public maintainer workflows.

## Update Rules

1. Structural repository changes must update the relevant internal project contracts in the same change set.
2. When user-facing behavior, project positioning, or onboarding guidance changes, update the public site pages in the same change set as needed.
3. Repository policy changes must be reflected in the appropriate `AGENTS.md` files and any affected public docs pages.
4. Public docs must not document repository-internal implementation details unless the detail is part of a stable public interface, user-visible behavior, or explicitly public maintainer workflow.

## Quality Gates

- Run the public-docs package test to verify broken links.
- Keep site navigation aligned with the stable clean routes.
- Use pull requests to review both internal and public documentation impacts together.
