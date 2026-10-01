# Protected credential publication recovery

Codex thread `PRRT_kwDORRAKg86nhybR` identified vault writes that survived
failed profile creation/publication without a public profile or SQL receipt to
drive cleanup. The server now synchronizes one private publication intent
before a network credential write. Its bounded, authenticated metadata binds
server, original actor/request/full public input, keyed credential commitment
and immutable owner/generation reference; it contains no credential bytes.

The shared account/vault gate admits at most one unpublished network generation
across profiles. An exact retry compares the original input and may recover the
same vault generation. A distinct request first compares the original SQL
receipt: an accepted publication clears only the intent; an authoritatively
absent publication enters durable cleanup state and removes its native
generation before permitting another write. Read or cleanup uncertainty retains
the original intent and blocks replacement. A cleanup-abandoned generation
cannot be resurrected under its original request ID. Published generations and
independently pinned selections remain intact.

Deterministic tests inject cancellation after vault success, a real temporary
SQLite insert failure, unavailable cleanup and a recreated coordinator. They
check exact input/secret retry binding, bounded repeated failed creates, lost
post-commit intent clearing and preservation of the selected original key.
Corrupt intent metadata must remain unchanged while blocking new native work.
These use only temporary state and an injected vault; native OS acceptance is
unperformed.

Focused server and CLI network race checks passed after the coordinator repair;
the store package compiled under the same filter with no matching tests. Go vet
passed across DeliDev. Final
combined command results after both review repairs are recorded separately;
earlier complete-Go failures remain historical evidence.
