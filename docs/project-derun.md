# Project: derun

## Goal
Provide a Go CLI that preserves terminal fidelity for AI-agent workflows and bridges MCP output transport.

## Project ID
`derun`

## Domain Ownership Map
- `cmds/derun`

## Domain Contract Documents
- `docs/cmds-derun-foundation.md`

## Cross-Domain Invariants
- CLI command identifiers and output contracts must remain stable for automation consumers.
- Terminal stream behavior must preserve ordering and ANSI compatibility by default.
- User-facing error messages must remain single-line and include deterministic `details` segments with safe diagnostic fields only (no secrets).
- User-facing error messages must preserve compatibility tokens used by MCP/automation integrations (`session not found`, `parse <field>`, `session_id is required`, `cursor is required`).
- Release artifact matrix and names must remain stable: `derun-linux-amd64.tar.gz`, `derun-darwin-amd64.tar.gz`, `derun-darwin-arm64.tar.gz`, `derun-windows-amd64.zip`.
- Homebrew distribution must install `derun` from GitHub release prebuilt archives (darwin amd64/arm64 and linux amd64) instead of source builds.

## Change Policy
- Update this index and `docs/cmds-derun-foundation.md` together whenever command shape or runtime contracts change.
- Update this index and `docs/cmds-derun-foundation.md` together whenever user-facing error message contracts or compatibility tokens change.
- Update `.github/workflows/release-derun.yml`, `scripts/release/update-homebrew.sh`, and `packaging/homebrew/templates/derun.rb.tmpl` in the same change when derun release artifact names, target matrix, or package-manager distribution contracts change.
- Align command lifecycle changes with `cmds/AGENTS.md` and root `AGENTS.md`.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
