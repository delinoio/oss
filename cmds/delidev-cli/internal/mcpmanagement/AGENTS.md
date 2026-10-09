# Worker-owned MCP management

- Follow `docs/cmds-delidev-harness-contract.md`, the credentials, catalog and storage contracts. Catalog support and native runtime support have separate capability ownership.
- Store definitions and protected credentials only on the original authenticated Worker. Public reads expose bounded configuration metadata and secret names, never values. Catalog browsing cannot launch MCP processes or authenticate implicitly.
- Retain immutable definition generations and actor-bound exact request receipts. Synchronize protected-reference ownership before writes and confirmed deletion intent before removals. Failed persistence fences the owner until original recovery.
- Current Agent references prevent catalog deletion. Accepted execution snapshots retain independently owned generation pins and protected references after catalog retirement. Only joined cleanup by the original dependent owner releases those pins.
- Keep structured logs limited to operation IDs, definition IDs, revisions and closed outcomes. Never log secrets, paths, native content or OAuth callback material.
