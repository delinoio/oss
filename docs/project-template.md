# Project Documentation Template

## Purpose
This document defines the mandatory structure for every `docs/project-*.md` file in this monorepo.
Use this template when creating a new project document.

## Required File Naming
- File name format: `docs/project-<project-id>.md`
- `project-id` must be lowercase kebab-case.
- `project-id` must be unique inside this repository.

## Required Sections
All project documents must include the sections below in this exact order.

## Goal
State why the project exists and what user problem it solves.

## Path
Declare canonical repository path(s) for the project.
Include all primary components if the project is split across multiple paths.

## Runtime and Language
Declare runtime and primary language.
Examples: `Rust CLI`, `Go CLI`, `Next.js 16 (TypeScript)`, `Expo React Native`.

## Users
List target users or operators.

## In Scope
List features that belong to this project now.

## Out of Scope
List features intentionally excluded from this project.

## Architecture
Describe high-level component boundaries and internal modules.

## Interfaces
Document public interfaces and contracts.
Use enum-style identifiers when possible.
Include route patterns, command shapes, API boundaries, and integration contracts.

## Storage
Document persistent data, cache, local files, and retention expectations.

## Security
Document trust boundaries, secrets handling, permissions, and data safety rules.

## Logging
Document required structured log fields and debugging expectations.
Include minimum operational logs for troubleshooting.
Prefer structured logging libraries (Go: `log/slog`, Rust: `tracing`) for operational and business events.

## Build and Test
Define local build commands, validation commands, and CI expectations.

## Roadmap
List phased milestones and future expansion directions.

## Open Questions
Track unresolved items that require explicit product or engineering decisions.

## Checklist for New Project Docs
- The file name uses `project-` prefix and kebab-case ID.
- All required sections are present.
- Paths in the doc exist or are explicitly marked as planned.
- Interfaces use stable enum-style identifiers where possible.
- Integration points reference canonical monorepo rules in `docs/monorepo.md`.
- The document is updated with every structural project change.
