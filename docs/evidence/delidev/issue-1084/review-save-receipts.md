# Accepted network save receipts after deletion

Codex thread `PRRT_kwDORRAKg86nk5mu` identifies an accepted save whose
live profile is deleted before an exact retry. The original durable receipt
still exists, but the shared reply reader previously converted the missing
resource into NotFound.

The reply reader now retains the accepted request/replay metadata and reports
`deleted = true` with no resource only for an authoritative NotFound read.
Authorization, cancellation and storage errors remain failures. It does not
resurrect an entity, rewrite credentials or change the receipt's input binding.

The authenticated regression creates and edits a credential-bearing profile,
deletes it, then replays both accepted saves. It checks original request IDs,
deleted/replayed responses, no resource recreation, no extra vault puts/deletes
and rejection of altered credential input.

- The individual receipt fixture passed freshly in 6.831 seconds.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server -run Network -count=1 -timeout 5m`
  passed all selected network tests in 28.085 seconds.

Both results exercise the repaired source. No unmodified pre-fix control result
is claimed. The complete combined Go command and final review inventory remain
for the end of this one-shot repair. Tests use private temporary SQLite/state,
loopback Connect and an injected vault; no real native credentials are used.
