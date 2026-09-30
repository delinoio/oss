# Review-repair main composition

The four review repairs and allocation documentation correction are retained at
`a61fcd567d095cff90a0aa0c5ee3ad0c14db3fe8`. Main advanced during the complete
local race run, producing new conflicts against the remote PR head. Merge main
`6d7004d2e37c42da5febd9dd3db06fb1fade019e` without rebasing before the single
review-repair push.

Five conflicts are append boundaries: frontend styles and frontend/domain/server/
Worker instructions. Preserve the complete terminal rules, including the new
64 KiB result/68 KiB journal bounds, loss-only cursor preservation and cleanup
problem precedence. Also preserve main's complete Home navigation and subscription
styles/rules, and its independently verified Claude failed-completion/Resume rules.
No incoming implementation is discarded. The production stylesheet contains
`.terminal-output`, `.subscription-settings`, `.sidebar-server-summary` and
`.github-integrations`.

Executed against the composed checkout:

- `VITEST_MAX_WORKERS=1 GOMAXPROCS=2 pnpm test` in `apps/delidev`: the complete
  command passed, including API-client generation/build, frontend typecheck,
  95 files / 1,222 tests, eight bundle checks, 16 launch/asset checks, widget
  fixtures and production build. Hydrate the required icon LFS asset first.
- Root `pnpm ci:contracts`: all 113 checks passed, including protocol allocation
  and semantic ownership, plus the incoming Go CI runner contracts.
- Root `GOCACHE=/private/tmp/issue-1088-go-cache GOMAXPROCS=2 go test -race -p 2
  -timeout 3m ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/server
  ./cmds/delidev-cli/internal/worker -run
  '^(TestTerminal|TestSessionArchiveWaitsForTerminal|TestWorkerPairingOwnership)'
  -count=1`: every selected package passed.
- Root `GOCACHE=/private/tmp/issue-1088-go-cache GOMAXPROCS=2 go vet -p 2
  ./cmds/delidev-cli/...`: passed.
- Conflict-marker and whitespace checks passed. Generated frontend and API-client
  `dist` directories are removed after validation.

The pre-merge complete race command finished with failures and package timeouts;
`review-full-race-2026-09-30.md` records the exact revision, watchdog and outcomes.
Do not describe the composed checkout as a passing full Go race suite. No full
race rerun follows this merge; its new evidence is the focused composition above.
Prior real Windows/Linux, physically remote Worker, native desktop visual and
release acceptance gaps remain unchanged.

The final remote inventory before the merge had no failing CI and four unresolved
Codex threads. All four are fixed in separate commits and may be resolved only
after the push succeeds. Pre-push CI success belongs to `6369089cc`; the new head
requires fresh checks. Codex separately reported exhausted code-review quota;
no fresh approval is inferred from an earlier review or from resolving threads.
