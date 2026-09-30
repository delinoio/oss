# Issue #1094 completed heartbeat race run, 2026-09-30

## Command and stable source

The full command started on the reconciled source recorded in
[the merge evidence](heartbeat-2026-09-30-1137.md) and completed after merge
`db01543d`. No Go source was changed during the run; the commit records the merge
resolution and validation evidence. Generated embedded assets and client output
were built before the run.

```sh
GOMAXPROCS=4 go test -race -p 2 -parallel 2 ./cmds/delidev-cli/... -timeout=20m
```

The command completed normally with exit code 1. Every package reported a result;
there was no early termination or package-watchdog panic. Nineteen tested packages
passed, three failed, and the command root and RPC package had no test files.

## Failed packages

- CLI: `TestCLISessionAcceptanceQueueAndArchive` failed at
  `sessions_test.go:281`. The workspace prepared successfully, but stale review
  submission returned an unavailable workspace reader instead of the expected
  conflict. Structured logs identify `git-diff`, comparison `creation`, and
  `recovery_required`. The isolated retry and its earlier review-context failure
  are recorded in the merge evidence; no further retry was performed.
- Grok harness: the `valid` case of
  `TestAPIInitializationOwnsConfigurationAndNativeAuthority` failed at
  `api_test.go:193` because Grok Build did not complete native initialization.
  The package completed in 1,109.942 seconds. Its source is identical to the
  merged main snapshot; this comparison alone does not establish the failure's
  cause.
- Workspace: the `worktree` case of
  `TestPRWorkspaceMatchReadsCurrentHeadWithoutTakingExecutionOwnership` failed
  at `pr_match_test.go:62` because the result did not prove the accepted
  preparation. `TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess`
  failed at `pr_match_test.go:161` with an unavailable operation timeout. The
  package completed in 1,145.161 seconds. This directory is unchanged from the
  merged main snapshot; no deadline or assertion was weakened.

The aggregate backend result remains non-green. The cause of these failures has
not been proved, and none is classified as an environmental or pre-existing
failure merely from timing or unchanged source.

## Passed packages and other validation

Full packages that passed were apiproxy, connections, credentials, domain,
forwarding, harness discovery, Claude harness, Codex harness, nativewire,
OpenCode harness, GitHub integrations, presentation, process, providers,
security, server, store, userservice and Worker. In particular, server passed
in 1,078.330 seconds, store in 299.666 seconds and Worker in 514.253 seconds.
These are full package results, distinct from the earlier filtered race run.

The merge evidence separately records the passing default desktop `pnpm test`
(100 files and 1,268 Vitest tests plus typecheck, fixtures and production build),
focused race regressions, Go vet, all 113 contract checks and full protocol
checks. Those passes do not replace the failed aggregate backend result.

No recorded validation processes remained after completion. Generated
repository-owned `dist` output is removed after normal commit hooks; dependency
caches are retained. Historical evidence is unchanged. Real-account,
native-platform and release acceptance remain unperformed. The remote head's
only reported check before this repair push was a successful Cloudflare check;
fresh CI and Codex review evidence must be assessed for the pushed head on a
later heartbeat.
