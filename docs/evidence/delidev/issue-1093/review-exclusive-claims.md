# Exclusive compaction send claims

Codex thread `PRRT_kwDORRAKg86nfo4i` identified that losing the outer job journal
could let a retained native claim be overwritten. Regression tests confirmed both
registration and command claims accepted replacement, and all eight concurrent
command writers succeeded (2.029 s failing package run).

The repair uses private create-if-absent semantics and synchronizes the claim file
and its parent before returning send authority. Existing identical, changed or
partial files fail closed and retain their bytes. Failed writes retain their claim
marker rather than creating a second send opportunity. Original interrupted outer
journals continue to return their original recovery receipt without execution.

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/worker -run
'^TestCompaction(Claims|CommandClaim|Journal)' -count=1` passed (2.857 s), including
one-writer concurrency, retained complete/partial evidence, private scope rejection
and interrupted-journal replay. No installed-native acceptance is claimed.
