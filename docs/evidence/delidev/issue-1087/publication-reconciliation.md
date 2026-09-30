# Browser publication reconciliation

Recorded on 2026-09-30 at `6f12b93ea107800624a85a517bcf459f4949e6b9`,
incorporating freshly fetched main
`d1f83cecee4e0c50ea094335392cf68845f85739`. Preserve all preceding evidence
at its original revision; the following results supersede no historical ledger.

## Composition

Preserve current main's Runs on/Runner Device copy, Activity filters, pinned
required-workflow evaluation, repository-inspection reservations and original
OpenCode event/global-root fixes. The sole documentation conflict contained
independent Activity and protected-browser instruction paragraphs; retain both.
Regenerate bindings from reconciled sources. No browser-owned Rust source or
native dependency declaration differs from the previously tested
`dbd18fdf1c9940c74ed1219435cb7027da9f8246` boundary.

## Executed checks

- `GOMAXPROCS=2 GOFLAGS=-p=1 pnpm test` in `apps/delidev` passed completely:
  all 1,273 Vitest tests across 100 files, client build, typechecking, eight
  packaging tests, sixteen desktop-launch tests, widget fixtures and final
  production frontend build. This is a passing default script at this boundary;
  earlier timing/setup failures and the passing serial run remain historical.
- Browser-focused Go race verification passed server and CLI packages with
  `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run TestBrowser -count=1`.
- The untrimmed fixture Go build passed before frontend integration execution.
- `pnpm proto:check` passed formatting/lint, breaking compatibility and generated
  freshness. Six focused protocol/structure tests passed.
- `git diff --check` passed; generated source has no unstaged drift.

The unchanged native sources retain their previous 32 passing tests and passing
all-target/all-feature Clippy results. Four native sidecar tests remain explicitly
ignored. The earlier complete broad Go failure and root Rust failures remain
validation limits at their recorded revisions; do not describe a passing full Go
or Rust workspace or unperformed provider, actual renderer, platform or release
acceptance. New PR heads and CI status must be assessed independently.

Remove generated repository-owned `dist` directories before leaving the worktree.
Temporary serial verification configuration is already removed and untracked.
