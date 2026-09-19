# async-commit-hook command ownership

- The common application service owns scheduling, final validation, acknowledgement, retention and comparison. CLI, Connect and MCP adapters must not implement competing decisions.
- Account lifecycle control remains in the canonical home configuration directory even when `--config` or `state_dir` changes. Tests must use isolated homes or explicit injected `Paths`; never touch developer credentials or real account state.
- Committed source is materialized from raw Git blobs. Never enable original hooks, filters, submodules or LFS implicitly. Hook installation discovery must not use the execution helper's disabled-hooks override.
- Persist the receipt before starting any worker. Queue order uses accepted sequence; cancellation/replacement cannot release a group before owned processes are reconciled by birth identity or a Windows Job Object.
- Report and log reads use owned artifact IDs and bounded, root-confined reads. Keep known-secret redaction before persistence, including streamed chunk boundaries and structured failures.
- Use synchronization barriers, not elapsed-time assertions, to prove asynchronous behavior in integration tests. Run `go test -race ./cmds/async-commit-hook/...` for lifecycle changes, and the root Go suite after generating administrator assets.
- Agent integration tests use isolated settings and preserve unrelated entries and comments. Keep the object-form MCP output schema compatible with supported clients.
