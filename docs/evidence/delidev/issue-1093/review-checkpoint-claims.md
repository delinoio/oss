# Original claims in checkpoint restoration

Codex thread `PRRT_kwDORRAKg86nfo4k` identified that restoration verified the
checkpoint and outer job journal without reading the original registration or send
claims. Source inspection confirmed those independent proofs were missing.

Checkpoint v2 now pins both canonical claim hashes. Registration retains the
original execution and request identity with the credential digest, and the send
claim joins that same registration to the action/execution. Retention and restoration
both require the private original files, canonical bytes, exact hashes and matching
identities before the native restoration gate. Missing, changed, partial, foreign,
symlinked or inconsistent proof fails closed. Unproven v1 files are preserved for
recovery and never silently upgraded or supplied with new claims.

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/worker -run
'^TestCompaction' -count=1` passed (8.789 s). The new metadata cases cover original
proof; missing and changed files; partial and symlinked files; foreign action,
execution, request and credentials; duplicate request identity; missing digests;
and v1 rejection. Identity cases separately exercise the joins after re-pinning
fixture hashes. Production independently checks the server-pinned checkpoint hash
and original completed journal before those joins.

These fixtures contain no accepted native snapshot. They prove claim rejection and
metadata joins, not installed-native restoration or replacement-process acceptance.
The original native acceptance gap remains in the separate acceptance-limit record.
