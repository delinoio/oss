# Preserve restore UUID reservations in deletion receipt paths

Addressed Codex thread `PRRT_kwDORRAKg86ngpTD` on PR #1222.
The finding is valid: generic Mutate/Replay guarded external restore UUIDs,
while custom session-deletion acceptance and recovered deletion/Worker receipts
could insert SQL receipts under a rolled-back or unjournaled restore UUID.

Centralize the existing gate-protected reservation check. Apply it after current
authorization and before permanent deletion intent acceptance, and in both
custom deletion receipt helpers, including startup and Worker acknowledgment
recovery. These helpers are Store methods so every direct receipt path consults
the same startup-loaded reservations without filesystem reads inside mutations.
The existing global UUID reservation contract is unchanged.

`GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/store
-run 'RestoreReservation|SessionDeletion' -count=1 -timeout 15m` passed
(23.586 s). The regressions use actual prepared-restore rollback and unjournaled
startup reservations, verify deletion acceptance leaves session/journal/receipts
unchanged, and reject conflicting retained session and Worker acknowledgement
receipt recovery. Existing permanent-deletion and acknowledgement replay cases
also passed. No real Worker filesystem cleanup or native account was invoked.
