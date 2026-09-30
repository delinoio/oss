# Codex child current-source telemetry repair

Review thread: `PRRT_kwDORRAKg86nkPES`, reviewed head `02d557b18`.

The finding is actionable. Reusing a retained child copied prior output and
observed-model fields into a collaboration receipt that did not supply them.
The repair clears output, observed model and usage before projecting each
collaboration item. The same omission boundary now applies to descendant-history
reads and child start/settings/terminal/message/usage notifications. Current
events contain only their own supplied fields; `childEvent` separately retains
last available values. Original requested-model identity stays immutable.

The new regression first reproduced stale telemetry for sendInput, wait and
closeAgent on the prior implementation. After repair,
`GOMAXPROCS=4 go test -race ./cmds/delidev-cli/internal/harness/codex -run Subagent
-count=1 -timeout=5m` passed. Tests cover omitted telemetry, unchanged retained
facts, fresh message replacement, separately supplied model/usage, late shutdown
and read-only descendant ownership. Owning instructions and the subagent contract
were updated together. This focused pass is distinct from full-suite and real
native/account/platform acceptance.

The thread is resolved only after the single final repair push succeeds.
