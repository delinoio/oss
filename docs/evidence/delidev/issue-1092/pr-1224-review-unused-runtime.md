# PR #1224: rejected source inspection cleanup

Codex review thread `PRRT_kwDORRAKg86ng0Y1` identified a fresh private runtime
left behind when native source-history inspection returned Unsupported.

The Worker now removes that proven-unused runtime after the source process has
closed, synchronizes the parent directory and confirms absence before preserving
the definite rejection. Failed removal or unconfirmed process closure returns
RecoveryRequired. Accepted inspection retains its runtime for native Fork.
Source native history and workspace ownership remain unchanged.

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/worker -run
'^TestSessionFork' -count=1 -timeout=3m` passed in 2.532 seconds. Real-filesystem
tests cover complete unused-runtime removal with source-history preservation,
accepted inspection and unjoined process retention, and inability to claim
successful removal through an invalid parent. Existing child-runtime deletion
ownership also passes. These tests use private temporary state and no inference.
