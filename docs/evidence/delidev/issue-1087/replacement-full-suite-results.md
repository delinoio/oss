# Replacement browser full-suite results

Recorded on 2026-09-30 for issue #1087. The implementation under test is
`dbd18fdf1c9940c74ed1219435cb7027da9f8246`; documentation-only commit
`8a0c71eb002097639d5a65ca058ec6a62dea8393` does not change these inputs.
Native, client and focused Go results remain in
`replacement-native-and-client-checks.md`. Later PR heads require independent
status; these results do not describe an untested reconciliation.

## Frontend

After preparing the integration fixtures' exact untrimmed Go build configuration,
the complete serial Vitest run passed all 1,252 tests across 98 files with no
skips. It used one worker, 30-second test/hook limits and a ten-second Testing
Library observation limit through temporary configuration. Assertions were
unchanged, and the temporary files were removed. Integration fixtures retain
their own bounded observation configuration. This establishes a passing serial
run, not a passing default `pnpm test`; its default failures remain recorded in
the preceding evidence file. Final production `pnpm build` passed.

## Broad Go

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/...` completed with failure.
It used one stable source boundary after reconciliation, rather than continuing
the invalidated earlier run.

- CLI failed `TestCLISessionAcceptanceQueueAndArchive`, with an unavailable
  creation-comparison workspace read.
- Grok failed `TestOriginalTextFootprintIsIndependentOfPublicationCopies` and
  `TestOwnedInputRetainsClaimsAndRejectsUncertainReplay`, then reached the
  ten-minute package deadline during
  `TestOriginalPlanControllerPreservesUncertainty/planning-valid`.
- Server reached the ten-minute package deadline during
  `TestGrokServerRejectsContentDriftWithoutPartialMutation/foreign-thread`.
- Workspace failed `TestWorkspaceDiffUnbornAndBoundedResults` and
  `TestPRWorkspaceMatchReadsCurrentHeadWithoutTakingExecutionOwnership`, then
  reached the ten-minute package deadline during
  `TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess`.
- API proxy, connections, credentials, domain, forwarding, harness core,
  Claude, Codex, native wire, OpenCode, GitHub integrations, presentation,
  process, providers, security, store, user service and Worker packages passed.

`GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` passed independently; the
race command's failure did not suppress this check. No unrelated test assertions
or native harness behavior were changed to obtain a passing result. The targeted
browser and migration race checks passed separately against schema 25.

The full Go result and required root Rust failures remain unresolved validation
limits, not passing checks. Controlled browser fixtures remain distinct from
unperformed live CEF/provider, Windows/X11 and release acceptance.
