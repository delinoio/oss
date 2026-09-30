# Issue #1088 main merge, 2026-09-30

## Revisions and composition

The maintenance pass merges main revision
`126641a6dcf274420c8c5800a6e88079f0e19990` into PR #1173's existing head
`285988291602f29712b2062860795a60e3242cff` without rebasing.

The single textual conflict is the append boundary in
`apps/delidev/src/styles.css`. Retain the terminal output rule and main's complete
GitHub Integrations presentation rules. Their selectors are independent. The
production stylesheet contains both `.terminal-output` and `.github-integrations`.
No terminal presentation behavior or Settings policy is changed by this repair.
The automatically composed session-deletion path still requires terminal cleanup
before removing session-owned PR activity. Existing terminal instructions and
both prior Codex review fixes remain present.

## Executed verification

All commands use the isolated issue worktree and temporary fixture state. The
consumed icon LFS object is hydrated before frontend checks. Required generated
API-client and frontend output is created explicitly for validation and removed
afterwards.

- `VITEST_MAX_WORKERS=1 GOMAXPROCS=2 pnpm test` from `apps/delidev`: passed the
  complete command, including API-client build, frontend typecheck, 89 unit-test
  files / 1,052 tests, eight packaging checks, 16 desktop launch/asset checks,
  Swift widget fixtures and the production build.
- An independent frontend `pnpm build`: passed. Inspection of its generated CSS
  confirms both resolved selector families are retained.
- Root `pnpm proto:check`: passed formatting/lint, semantic breaking checks and
  complete regeneration with no generated-source drift relative to the composed
  index. Main's PR activity fields and the terminal declarations remain separate.
- `node --test scripts/ci/delidev-proto.test.mjs`: all three allocation, ownership
  and semantic-comparison checks passed.
- Root `pnpm ci:contracts`: all 102 checks passed.
- Root `GOCACHE=<temporary isolated cache> GOMAXPROCS=2 go vet -p 2
  ./cmds/delidev-cli/...`: passed.
- Root race-enabled focused store/server/Worker terminal suites passed with
  `-p 2 -timeout=3m -count=1` and test selection
  `^(TestTerminal|TestSessionArchiveWaitsForTerminal|TestWorkerPairingOwnership)`.
  Coverage includes both Archive cleanup orders, error rollback/exact receipt
  retry, journal bounds/exact finished report retry, Worker receipt loss and
  negotiated capability/duplicate validation.

## Limits

The frontend pass is new evidence for this composed revision. Preserve the prior
broad Go/native failures and original evidence limits in `validation.md`; this
pass does not claim a complete Go race-suite rerun or real Windows/Linux,
physically remote Worker, native desktop visual or release acceptance.
The conflict repair changes CSS only; no Rust source is edited.

## References

- [PR #1173](https://github.com/delinoio/oss/pull/1173)
- [Prior implementation and review verification](validation.md)
