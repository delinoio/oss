# DeliDev CLI

- Follow `docs/project-delidev.md`, `docs/cmds-delidev-contract.md`, `docs/protos-delidev-v1-contract.md`, and the complete issue #964 requirements snapshot.
- Keep the evidence ledger current. Fixture success, cross-compilation, or explicit unsupported results do not prove real-harness/platform acceptance.
- The executable is `delidev`; ordinary commands cannot implicitly start the server. Server/sidecar behavior is identical.
- Keep business logic in Go and product communication in authenticated Connect. Workers initiate outbound connections; never add a client-facing WebSocket or SSE API.
- Use private temporary state/accounts/repositories in tests. Never access user logins, redeem credits, publish to GitHub, or invoke inference from ordinary tests.
- Preserve durable request receipts, typed revision checks, atomic state/events/routing, independent outcome/archive/recovery, uncertainty before retries, and deletion tombstones.
- No harness installation, account failover, TTY scraping, unsupported-feature emulation, unprotected secrets, or raw provider diagnostics.
- Run package Go tests and vet. Use real temporary SQLite/Git/process resources for integration tests; generate protocol bindings from the schema.
- Follow `docs/cmds-delidev-workspace-contract.md` for Worker Git work. Never fetch during inspection or Local preparation, silently use stale remote refs after fetch failure, run repository hooks during managed preparation, or delete an original Local checkout. Keep partial cleanup retryable and distinguish prepared workspace tests from complete session/process recovery.
- Pairing codes are single-use/expiring; raw codes and device tokens stay out of SQLite and ordinary output. Workers have machine-scoped authorization and initiate outbound Connect streams. Revalidate revocation and job ownership at mutation commit boundaries, and retain uncertainty after a disconnected or replaced Worker instance instead of redispatching accepted execution.
