# Protected browser replacement verification

Recorded on 2026-09-30 for issue #1087. The replacement branch starts from freshly
fetched main `ad0e3e9a29cb3d8375ab5d168bb160c35a023250` in
`/Users/kdy1/.codex/worktrees/055c/oss`. It reconciles the scoped browser change
from closed, unmerged PR #1201 at `448a5a09b852caf3b28020deeb5c81d79c5fc8b6`.
The two earlier issue-1087 evidence files retain their original source revisions
and historical qualifications; their results are not new validation of this branch.

## Composition and repairs

- Preserve current main's subscription/settings/sidebar/schedule presentation and
  permanent session deletion. Add independent BrowserService declarations and
  regenerate Go, TypeScript and legacy reflection facades from the current split
  schemas. No existing-message field, shared enum reservation or migration version
  is consumed; SQLite remains schema 24.
- Retain the conversation/composer beside raw privilege-free CEF children and
  protected server/device/account request-context paths. Device-local tabs and
  web credentials never enter product storage, synchronization or workspaces.
- Visible-dialog detection ignores the persistent closed compact drawer and the
  open nonmodal wide sidebar region. Three component regressions exercise opening,
  hiding for an active dialog and reopening with a distinct presentation identity.
- Saved-connection purge staging follows fallible window setup. The private intent
  retains exact connection/pairing/removal identity, and full-directory purge
  requires independently completed native shutdown plus Go's retained original
  Removing/Removed receipt. Positive unchanged Paired evidence cancels an
  unaccepted intent; unknown acceptance and independent account deletion remain
  pending. The controlled fixture preserves profile bytes before acceptance.
- Browser cleanup uses an independent read-only controller with a two-second
  joined-child deadline, retaining interactive controller availability while a
  saved endpoint is offline. A native fixture holds the interactive gate while
  checking observer timeout and child/stream completion.

## Executed checks at the initial implementation boundary

All fixtures use isolated temporary state and no user credentials or inference.

- `pnpm install` succeeded and installed the existing linked-worktree hooks.
- `git lfs pull` and `git lfs fsck` succeeded before consumed asset preparation.
- `pnpm proto:generate`, frontend/client typechecking and production frontend build
  passed. `pnpm proto:lint` and `pnpm proto:breaking` passed.
- `node scripts/delidev/verify-independent-changes.mjs` passed.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`
  passed all six allocation, compatibility and structure checks.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run TestBrowser -count=1`
  passed the five server scenarios and the CLI scenario. These exercise sharing,
  scope isolation, restart, receipts, Archive/session retention, offline cleanup,
  exact acknowledgment, authorization, hostile origins and concurrent registration.
- Focused default frontend validation passed all nine browser/cleanup tests, while
  the concurrent App file failed 28 of 44 tests under severe host load.
- Serial affected-file verification with one worker and a 30-second test limit
  passed all 53 tests in Browser, account cleanup and App. No assertions changed.
- Required default `pnpm test` completed its Vitest stage with 1,170 passing and
  75 failing tests across 97 files. Failures include observation and five-second
  test deadlines; this is not a passing full frontend run. A separate serial run
  uses temporary verification configuration with the same assertions, one worker,
  a 30-second test/hook limit and a ten-second Testing Library observation limit.
  That temporary configuration is excluded from commits and removed after use.
- Initial default Go race/vet compilation and fresh-target root Rust compilation
  were interrupted during host load above 630. Bounded-concurrency Go and cached
  Rust runs replace those attempts. The first native-feature compile reached
  Tauri's resource check before sidecar preparation completed and rejected the
  absent generated binary; the ordered retry waits for preparation.

Remaining native, serial full-suite, broad Go, vet, root Rust and protocol freshness
results will be recorded in a separate final verification record after completion.
Pending checks are not reported as passed.

## Acceptance limits

Controlled fixtures and compilation do not prove actual provider login,
history/password behavior, a live renderer shutdown/flush race, Windows/X11
rendering, installed packages or signed release acceptance. Those observations
remain separate from implementation verification. Offline and uncertain cleanup
never imply completed server acknowledgment. The pinned Tauri/CEF revision is
unchanged.
