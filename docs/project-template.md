# Project Index Template

## Purpose
This template defines the required structure for every `docs/project-<project-id>.md` file.
A project index document is the canonical entry point for ownership, domain boundaries, and cross-domain invariants.

## Required File Naming
- File name format: `docs/project-<project-id>.md`
- `project-id` must be lowercase kebab-case.
- `project-id` must be unique inside this repository.

## Required Sections
All project index documents must include the sections below in this exact order.

## Goal
State why the project exists and what user or operator problem it solves.

## Project ID
Declare the stable enum-like project identifier.

## Domain Ownership Map
Declare canonical repository paths grouped by domain (`apps`, `cmds`, `servers`, `crates`, `protos`, `packages`).
If the project has multiple deployables, list each component explicitly.

## Domain Contract Documents
List all domain contract documents that define runtime behavior and interfaces.
Use direct links to `docs/<domain>-<project-or-component>-<contract>.md` files.

## Cross-Domain Invariants
Document rules that must remain consistent across domain boundaries.
Include route IDs, component IDs, protocol ownership, and compatibility rules when relevant.

## Change Policy
Document which documents must be updated together when contracts, ownership, or interfaces change.

## References
Link to related project index documents, templates, and other canonical contracts.

## Checklist for New Project Index Docs
- The file name uses `project-` prefix and kebab-case ID.
- All required sections are present and in order.
- The ownership map uses canonical repository paths.
- At least one domain contract document exists.
- Cross-domain invariants use stable enum-like identifiers where possible.
- The index is updated in the same change set as structural contract changes.
