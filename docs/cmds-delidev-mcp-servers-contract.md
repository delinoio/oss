# DeliDev Worker-owned MCP server definitions

## Scope

Issue [#2129](https://github.com/delinoio/oss/issues/2129) adds Worker-owned MCP management to Settings. This document owns the first slice only: the stored definition resource and its Settings editor. Go owns validation and storage in `cmds/delidev-cli`; `protos/delidev/v1` owns the additive entity kind; `apps/delidev` owns the editor. Agent Worker selection, authentication, secret references, per-harness eligibility and execution remain unimplemented and are tracked by #2129 and its adapter issues.

## Runtime and Language

Go server and generated Protocol Buffers/Connect bindings; React desktop client.

## Users and Operators

The authenticated owner manages definitions on the selected server through the existing configuration operations.

## Interfaces and Contracts

- `EntityKind.MCP_SERVER` (wire number 36, reserved for issue 2129 in `protos/delidev/allocations.json`) maps to the domain kind `mcp_server`. It uses `ConfigurationService.SaveConfiguration` and `DeleteConfiguration` with schema version 1 and revision-checked mutations, and `ResourceService` reads.
- The closed document is `{name, transport, enabled, command?, args?, working_directory?, url?}`. Unknown fields and duplicate keys are rejected.
- `transport` is `stdio` or `streamable_http`. `stdio` requires a nonempty `command`, allows at most 64 arguments and an optional absolute `working_directory`, and rejects `url`. `streamable_http` requires an HTTP or HTTPS `url` without credentials or fragment and rejects the local command fields.
- The document carries no secret material. Environment values, headers and OAuth credentials are out of this slice; they must use protected Go-owned references when added.
- Saving, listing or browsing a definition never starts a server, signs in or runs inference.
- Deletion uses the generic configuration deletion. Blocking deletion while Agent Workers reference a definition is added together with Agent Worker selection.
- The MCP Settings category follows Instructions in the AI group and reuses the generic configuration list and editor. Portable configuration export and import do not include MCP definitions yet.

## Storage

Definitions use the existing resource store and require no SQLite migration.

## Logging and Observability

Configuration mutations use the existing structured configuration logs; definitions contain no secrets to redact.

## Build and Test

`go test ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/server` and `pnpm test` in `apps/delidev`.

## Roadmap

Agent Worker selections, authentication and secret references, per-harness eligibility, runtime adoption for Codex, Claude Code, OpenCode and Grok, reference-blocked deletion and portable export remain open under #2129.
