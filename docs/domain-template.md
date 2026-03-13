# Domain Contract Template

## Purpose
This template defines runtime and integration contracts for a project component inside a single domain.
Use this template for files like `docs/apps-<...>.md`, `docs/cmds-<...>.md`, `docs/servers-<...>.md`, `docs/crates-<...>.md`, `docs/protos-<...>.md`, or `docs/packages-<...>.md`.

## Required File Naming
- File name format: `docs/<domain>-<project-or-component>-<contract>.md`
- `<domain>` must be one of: `apps`, `cmds`, `servers`, `crates`, `protos`, `packages`.
- `<project-or-component>` must be lowercase kebab-case.
- `<contract>` must describe the contract purpose (for example: `foundation`, `api-contract`, `language-contract`).

## Required Sections
All domain contract documents must include the sections below in this exact order.

## Scope
Declare the project/component and canonical implementation paths owned by this document.

## Runtime and Language
Declare runtime and primary language.

## Users and Operators
List primary users, operators, or system actors for this component.

## Interfaces and Contracts
Document public interfaces and stable identifiers.
Include route patterns, command shapes, RPC/API contracts, and component integration boundaries.

## Storage
Document persistent data, cache, and local file contracts.

## Security
Document trust boundaries, secrets handling, authorization, and data safety constraints.

## Logging
Document required structured logs and troubleshooting expectations.

## Build and Test
Define local validation commands and CI expectations for this component.

## Dependencies and Integrations
Document upstream/downstream dependencies and cross-domain dependencies.

## Change Triggers
Declare what related docs and AGENTS contracts must be updated when this contract changes.

## References
Link to the owning `docs/project-<id>.md` index and any related contract docs.

## Checklist for Domain Contract Docs
- The file name follows domain prefix rules.
- All required sections are present and in order.
- Interfaces use stable identifiers where possible.
- Storage, security, logging, and test expectations are explicit.
- Change triggers include related project/domain docs.
