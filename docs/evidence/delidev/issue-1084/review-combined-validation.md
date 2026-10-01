# Combined validation after Codex review repairs

## Source and scope

The response metadata repair is `5786bc60`; the protected publication repair
is `b7547b9f`. Their independent reasoning and fixture evidence are in
`review-response-metadata.md` and `review-profile-publication.md`.

The complete Go command began on `561f13f0d3926f9736fcc6955fe27db27e7ef458`.
That unpublished commit was amended only to document the new cleanup log names,
producing `b7547b9f`. The Go source and generated Go bindings are identical
between those two commits. The final follow-up commit changes evidence only.

## Passing checks

- `GOMAXPROCS=2 go test -race -p 2` across outbound, providers,
  inference-proxy and GitHub packages, `-count=1 -timeout 5m`, passed after
  the response metadata repair. A subsequent outbound run including the
  short field-name boundary regression passed.
- `GOMAXPROCS=2 go test -race -p 2` across server, CLI and store packages,
  filter `Network|CLINetwork`, `-count=1 -timeout 5m`, passed all matched
  server/CLI tests. Store compiled with no matching tests.
- The final `NetworkSaveIntent` server race run passed all selected cases
  after private-root/file checks were added.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed after both repairs.

## Complete Go command

`GOMAXPROCS=2 go test -race -p 8 ./cmds/delidev-cli/... -timeout 20m`
completed with exit 0: all 23 packages passed, with 11 unchanged package
results reused from Go's test cache. The focused review regressions above
used `-count=1`. No data-race warning appears in this command's log.

The previously failing CLI package passed in 271.879 seconds, Grok in
1,004.511 seconds, server in 857.709 seconds and workspace in 649.101 seconds.
Worker passed in 378.209 seconds and OpenCode in 71.519 seconds. Outbound,
provider, inference-proxy and GitHub packages all ran and passed. This is a
passing complete command on the repaired source, not a diagnosed fix for the
earlier workspace proof failures or exhausted package budgets.

## Preserved limits

The pre-review complete Go run failed with 19 passing/four failing packages;
its exact failures remain in `main-d1f83cec-local-validation.md`. No assertion
or observation deadline was relaxed, and the cause of the earlier CLI
branch/baseline comparison remains unresolved. Passing a later CLI run does
not establish that either credential repair fixed its workspace failure.

The merge's protocol lint/breaking/forced-regeneration and seven CI fixtures
passed before these repairs; protocol and generator inputs are unchanged by
the repairs. Preceding frontend (98 files/1,264 tests plus build/asset checks)
and client (typecheck/45 tests) results remain evidence for their unchanged
inputs, not new executions. Generated frontend/client `dist` outputs are absent
and untracked.

Tests used loopback fixtures, temporary SQLite/private state and injected
vaults. Real provider/GitHub accounts, enterprise proxies, native credential
lifecycle, Windows/Linux runtime, release and Worker bootstrap acceptance were
not performed. Fresh CI and Codex review are required for the final pushed head;
earlier-head checks and the addressed review do not certify it.
