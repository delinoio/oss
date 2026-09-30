# Issue #1094 post-review validation, 2026-09-30

## Source and review repairs

Validation used stable runtime source
`e0b0e96ab68165c0c40b3070e3819bc9ed4ea1f0`, including four independent fixes:

- `379f91f6`: correlated benign Codex root status events preserve live-child cleanup.
- `011b44d1`: Claude retains original idle proof until every owned child settles.
- `338a5034`: acknowledged Claude child task receipts retain native event identities.
- `e0b0e96a`: complete ownership validation reserves each Claude parent tool for one child.

Each repair's independent evidence records its review thread and focused passing
race regressions. No Go source was modified during the aggregate run below.
Historical implementation and earlier failed-run records remain unchanged.

## Aggregate backend result

```sh
GOMAXPROCS=4 go test -race -p 2 -parallel 2 ./cmds/delidev-cli/... -timeout=20m
```

The command completed with exit code 1. All packages reported results without
early termination or package-watchdog panic. Twenty-one tested packages passed:
seven executed fresh and fourteen used Go's verified test cache. The command root
and RPC package had no test files. CLI was the sole failed package.

Fresh passes were connections (22.208 seconds), domain (1.973), Codex harness
(122.665), Grok harness (970.119), server (590.407), Worker (314.158) and workspace
(444.825). In particular, the complete repaired Worker package passed, including
the new terminal, acknowledgment and parent-tool ownership regressions.

CLI's `TestCLISessionAcceptanceQueueAndArchive` failed at `sessions_test.go:286`:
explicit stale-review submission returned an unavailable workspace reader rather
than succeeding. This is later than the pre-review failure at line 281 and the
isolated review-context failure at line 244. The command's backend result remains
non-green. No deadline or assertion was weakened; the cause remains unproved and
is not classified as environmental or pre-existing. The earlier Grok and
workspace failures did not recur in this final run.

## Other checks and boundaries

After the four repairs, all 113 `pnpm ci:contracts` checks,
`GOMAXPROCS=4 go vet ./cmds/delidev-cli/...`, full `pnpm proto:check` and
`git diff --check` passed. Protocol generation reproduced without drift.
Required client and embedded outputs were explicitly rebuilt first.

The default desktop `GOMAXPROCS=2 pnpm test` pass remains recorded in the
[merge evidence](heartbeat-2026-09-30-1137.md): 100 Vitest files, 1,268 tests,
typecheck, bundle/launch/widget fixtures and production build. The review repairs
change no frontend runtime or generated client source, so that evidence is not
replaced with an unperformed repeated frontend run.

No validation processes remained after aggregate completion. Generated
repository-owned `dist` output is removed after normal hooks; dependency caches
are preserved. Real-account, native-platform and release acceptance remain
unperformed. The PR was verified open, non-draft and still at the original remote
head before the single repair push. The four addressed review threads are
resolved only after that push succeeds; fresh CI and review evidence belongs to
the new head and is assessed separately by the existing heartbeat.
