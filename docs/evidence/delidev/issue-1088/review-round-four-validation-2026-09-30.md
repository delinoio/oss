# Terminal maintenance round four validation

Runtime revision: `130be83f1235093890ffd6211de3ae105dcb73c2`, PR #1226. The concluding documentation commit changes no runtime source. Main `9efb1917e0127a9223cee0969877238ab37c0e1d` was merged in `fb6aa4d4c`; shutdown output loss was repaired in `2c0238a3b`, and acknowledged ownership retirement in `130be83f1`. Each repair has a separate evidence record.

## Executed validation

- Root `GOMAXPROCS=2 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...`: passed, exit 0. All test-bearing packages passed, including CLI (176.055s), harness discovery (15.637s), Claude (76.064s), Codex (71.035s), Grok (932.498s), OpenCode (29.582s), server (676.388s), store (256.557s), Worker (141.200s) and workspace (388.151s). Other package results used valid Go cache entries where indicated. The package watchdog and native ownership deadlines were not weakened.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...`: passed.
- `pnpm proto:check`: passed lint, breaking compatibility and generated-source freshness.
- `pnpm ci:contracts`: 113 passed, 0 failed.
- API client build and test: 5 files / 46 tests passed.
- `pnpm prepare:assets` followed by `GOMAXPROCS=2 pnpm test` in `apps/delidev`: passed type checking, 100 files / 1,280 tests, 8 packaging fixtures, 16 launcher/asset fixtures, Swift widget fixtures and production build. Default fixture deadlines were unchanged.
- Focused `^TestTerminal` Worker race tests: three repetitions passed in 11.418s. Shutdown/replacement/native receipt tests separately passed three repetitions in 17.188s.
- Windows amd64 Worker test binary cross-compilation passed with `GOMAXPROCS=2 GOOS=windows GOARCH=amd64 go test -p 2 -c`. This is compilation evidence only.
- Required LFS icon was hydrated. Generated app/client `dist` directories were removed after validation; a repository-owned directory scan found none remaining. `git diff --check` passed. No Rust source changed.

The earlier failed full-suite runs remain preserved in their original evidence records. This successful run is new evidence and does not establish a cause for those earlier failures or a passing baseline comparison.

## Review and remote limits

The shutdown-loss and ownership-retirement findings are fixed; their threads may be resolved only after the final push succeeds. `PRRT_kwDORRAKg86nk487` remains unresolved pending the recovery-authority choice documented in `revocation-cleanup-authority-blocker-2026-09-30.md`. Its original revoked credential and machine/device ownership checks remain enforced.

The initial remote inventory on old head `dbfbd8f5094f82afcf94b7a09f579bcb91401a91` contained one successful Cloudflare Pages check and no GitHub Actions results. A push invalidates that head's CI/review evidence. Fresh checks and reviews belong to the next scheduled maintenance pass; local success does not establish merge readiness or approval.

Controlled macOS fixtures, generated-source checks and Windows cross-compilation do not establish native Windows/Linux, real remote Worker, desktop visual or release acceptance. No merge or auto-merge action was performed.
