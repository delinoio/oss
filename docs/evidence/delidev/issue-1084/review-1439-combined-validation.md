# Review maintenance combined validation

Issue #1084 / PR #1216. Original reviewed head: `b0d4092b1575f9e8b543f1add4494ccfd9062637`. Combined source tested: `56d75fc10f569203b3080668b5c93dcd11a7493c`, including main `9efb1917e0127a9223cee0969877238ab37c0e1d`. Execution crossed 2026-09-30 UTC / 2026-10-01 Asia/Seoul. The final evidence commit changes documentation only.

## Repairs

- `42a8e3aa6`: accepted Save receipts survive later deletion without recreating a profile or rewriting credentials. See [receipt evidence](review-save-receipts.md).
- `84417c1d5`: durable server-owned deletion cleanup survives native failures, cancellation, actor revocation and restart. See [cleanup evidence](review-delete-cleanup.md).
- `c054a882d`: plaintext loopback requests require Direct or an explicit bypass before connection; verified HTTPS proxy routing remains supported. See [security evidence](review-plaintext-loopback.md).
- `2a7fffcf7`: main merge preserves proxy/switch/restore capabilities, both CLI timeout rules and regenerated bindings. See [merge evidence](main-9efb-merge.md).
- `56d75fc10`: managed restore preserves current network bodies/generations/selections with ordinary revision freshening and refuses unfinished private intents under the shared credential gate. See [composition evidence](managed-restore-network-authority.md).

## Completed passing checks

- Independent focused race commands and every regression are recorded in those separate evidence files. The combined server/store network/restore checks passed, as did provider, inference, outbound and GitHub proxy checks.
- `pnpm proto:check`: formatting, lint, main-baseline breaking comparison and forced generation/freshness passed after the merge. The initial missing-main compatibility failure is preserved in the merge record; no protocol policy was weakened.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...`: passed.
- `GOMAXPROCS=2 go build -p 2 -o /tmp/delidev-1084-e6e9-review-1439-binary ./cmds/delidev-cli`: passed.
- `pnpm --dir packages/delidev-api-client typecheck`: passed; `pnpm --dir packages/delidev-api-client test --maxWorkers=2`: 6 files / 47 tests passed.
- `GOMAXPROCS=2 pnpm test` from `apps/delidev`: client build, typecheck, 99 files / 1,275 tests, 8 bundle fixtures, 16 desktop-launch/assets fixtures, widget checks and Rsbuild production build passed. The temporary Vitest worker cap of 2 was restored byte-for-byte after the command. Required icon LFS content was hydrated. Generated desktop/client `dist` directories were removed after consumption.
- `git diff --check`: passed.

## Broad Go run: failed, not a clean bill of health

`GOMAXPROCS=2 go test -race -p 8 ./cmds/delidev-cli/... -timeout=20m` completed with exit 1: 21 passing packages (8 cached, 13 fresh) and 2 failing packages. No data-race warning or package timeout was reported.

1. CLI: `TestCLISessionAcceptanceQueueAndArchive`, 38.83s, failed at `sessions_test.go:239` for a creation-comparison diff with workspace reader `unavailable`; the Worker classified the original `git-diff` read as `recovery_required`. CLI package duration: 202.129s. This matches the test/source/diagnostic already preserved in [the earlier d1f83cec run](main-d1f83cec-local-validation.md). Its branch/baseline comparison remains unresolved. Recurrence is not proof of cause or proof that it is unrelated to this branch; no repair or assertion/deadline relaxation was attempted without new diagnostic evidence.
2. Grok: `TestAPIInitializationOwnsConfigurationAndNativeAuthority/valid` failed at `api_test.go:193`, `unavailable: Grok Build did not complete native initialization`, after 10.39s. The enclosing test took 27.80s; package duration: 1037.991s. `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/harness/grok -run '^TestAPIInitializationOwnsConfigurationAndNativeAuthority$' -count=1 -timeout=3m` then passed in isolation, 19.967s. Grok source has no diff from the merged main revision in this PR. The isolated pass does not diagnose or fix the broad-run failure, and the broad run remains failed.

Fresh passing broad package results included server 929.471s, store 522.578s, Worker 382.232s, apiproxy 7.283s, providers 6.103s, outbound 1.809s and GitHub 15.258s. Cached native-wire success does not diagnose its separately retained historical asynchronous log-buffer race. Incoming main's independently documented Windows Grok probe deadline remains main's change, not a proxy repair.

## Evidence limits and maintenance

CI and Codex acceptance must be assessed on the newly pushed head. Earlier green CI/reactions certify earlier heads only. These temporary-state, controlled-peer and injected-vault checks do not establish real provider/GitHub account, enterprise proxy, native credential lifecycle, supported-platform runtime/release or Worker bootstrap acceptance. Earlier failures, incomplete comparisons and acceptance limits remain visible. The existing heartbeat continues without merging or enabling auto-merge; known blocked repairs are not repeated without new evidence.
