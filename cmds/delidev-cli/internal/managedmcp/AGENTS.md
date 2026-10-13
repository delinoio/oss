# Managed MCP working instructions

- Follow root and parent instructions. This directory owns private Worker managed-MCP catalog, authentication and execution-time resolution (`runtime.go`) code. Resolution is read-only: it verifies frozen generations against the immutable catalog and reads protected values for one execution, never launching, refreshing or changing anything.
- Read the [managed MCP contract](../../../../docs/cmds-delidev-managed-mcp-contract.md) and affected credential, storage and harness contracts before changes.
