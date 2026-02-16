# Project: derun

## Goal
`derun` is a Go CLI that helps teams run AI coding-agent workflows consistently.
It standardizes task execution, context loading, and repeatable command orchestration for agent-driven development.

## Path
- `cmds/derun`

## Runtime and Language
- Go CLI

## Users
- Engineers using coding agents in local repositories
- Automation maintainers who need deterministic agent execution wrappers

## In Scope
- Define and run reusable AI-agent task workflows
- Load workflow configuration from repository-local files
- Normalize execution environment and command invocation
- Provide structured logging and error traces for debugging

## Out of Scope
- Hosting model inference directly
- Replacing editor integrations
- Managing deployment infrastructure

## Architecture
- CLI parser resolves subcommands and options.
- Workflow loader parses local workflow config.
- Runner executes configured steps with controlled environment.
- Reporter emits structured logs and status summaries.

## Interfaces
Canonical command identifiers:

```ts
enum DerunCommand {
  Init = "init",
  Run = "run",
  List = "list",
  Validate = "validate",
}
```

Canonical workflow execution status values:

```ts
enum DerunRunStatus {
  Pending = "pending",
  Running = "running",
  Succeeded = "succeeded",
  Failed = "failed",
  Canceled = "canceled",
}
```

Planned config contract (high-level):
- Workflow name
- Ordered steps
- Environment variables (non-secret and secret references)
- Failure policy

## Storage
- Local config files inside repository.
- Optional local cache for workflow metadata.
- No central server dependency required for baseline execution.

## Security
- Never print secret values in logs.
- Support explicit allowlist for inherited environment variables.
- Fail closed on invalid workflow definitions.

## Logging
Required baseline logs:
- Selected workflow and resolved config path
- Step lifecycle transitions
- Exit codes and failure context
- Total run duration and final status

## Build and Test
Planned commands:
- Build: `go build ./cmds/derun/...`
- Test: `go test ./cmds/derun/...`
- Full Go validation: `go test ./...`

## Roadmap
- Phase 1: Core workflow execution and validation.
- Phase 2: Rich step templates and environment profiles.
- Phase 3: Remote status reporting adapters.
- Phase 4: Team policy enforcement and reusable workflow libraries.

## Open Questions
- Final configuration file name and schema format.
- Required minimum set of built-in step types.
- Policy for interactive prompts in non-TTY contexts.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
