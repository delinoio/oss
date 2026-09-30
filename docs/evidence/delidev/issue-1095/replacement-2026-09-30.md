# Issue #1095 replacement on current main

## Revision and scope

The initial clean checkout and freshly fetched main were
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250`. Issue #1095 remained open;
PRs #1124 and #1174 were closed without merging, and no matching open PR existed.
The replacement retains the implementation and historical observations from
closed PR #1174 at `9493142c91ac7e25af43cc75bcb823a1033428eb` and composes it with
main's session deletion, forwarding, title and scoped ownership contracts.
The historical [validation record](validation.md) and
[PR #1124 observations](previous-pr-1124.md) are preserved unchanged.

Integration commit `15a5fd1c5` regenerates bindings from the reconciled schemas.
It retains the already reserved managed Worker capability value 3. The unrelated
historical frontend timing edits are excluded. Main subsequently advanced to
`574c1a92c957fc741a723ff8123888dad32a2194`; merge commit
`666350ecbd0eff503e7dbf364560ce24bd8f4942` preserves its Grok accounting schema 25,
independent capability and compaction reservations. All documentation conflicts
retain both domains' source-backed requirements, and generated output is
regenerated rather than selected from either side. This feature adds no migration.

## Native envelope and regression fixes

The official Codex `rust-v0.151.0` account protocol and authentication storage
were read through authenticated `gh api`. The pinned login-completed notification
contains nullable `onboardingEntrypoint`; the previous strict decoder rejected
the ordinary null envelope. A controlled fixture reproduced that rejection.
The adapter now accepts null and the pinned closed `life_sciences` enum, rejects
unknown values, and still requires the original successful native login identity.
No onboarding flow or authority is inferred from this presentation field.

Four additional findings posted on the closed prior PR were evaluated against
this replacement and fixed independently:

- `d4fc1fed2` preserves protected execution delivery/completion uncertainty through
  workspace cleanup, stops ordinary job reporting, retains the started claim,
  and closes the primary work lane so lost ownership becomes recovery-required.
  Controlled Take and Finish transport failures cannot publish an ordinary
  completed job or erase the original claim journal.
- `7876f763f` rejects subscription dispatch before consuming queued input when
  the selected Worker has not negotiated the managed capability. A real temporary
  Connect/SQLite fixture covers both unsupported and negotiated admission.
- `fc65e374` checks retained native history for raw credentials and padded or
  unpadded standard/URL Base64 copies. All four encoded cases failed before the
  fix; cleanup now remains unconfirmed while retaining original history.
- `c4a2950b081ca1a830e91a43600c7591ea2e7836` rechecks the actual execution workspace's
  merged managed configuration before both thread start and resume. Both native
  mutation cases accepted a foreign provider override before the fix; neither
  mutation is sent after rejection, and the official provider cases still pass.

## Executed checks

Checks use Go 1.26.8, Node.js 24.20.0 and pnpm 10.26.2 on macOS arm64. Fixtures
use synthetic credentials and private temporary state, never user logins.
The final composed implementation revision is `666350ecbd0eff503e7dbf364560ce24bd8f4942`.

- `GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/worker
  -run '^TestManagedExecutionPreNativeFailureRemovesAuthentication$' -count=1`:
  passed, including uncertain protected Take/Finish and retained claim cases.
- `GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/server
  -run '^TestSubscriptionDispatchRequiresManagedWorkerCapability$' -count=1`:
  passed both capability admission cases.
- `GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/worker
  -run '^TestManagedExecutionCleanupRejectsEncodedAuthentication$' -count=1`:
  passed the clean, raw and four encoded-remnant cases.
- `GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/harness/codex
  -run '^(TestManagedCodexThreadRechecksWorkspaceProviderAuthority|TestManagedCodexDeviceLoginRefreshAndLocalLogout)$'
  -count=1`: passed workspace start/resume, native login envelope, rotation,
  unchanged-refresh rejection and local logout cases.
- `pnpm proto:check`: passed formatting/lint, breaking comparison against main
  `574c1a92c` and forced generation freshness with no drift.
- `GOMAXPROCS=4 go test -p 1 ./protos/...`: passed all binding/reflection packages.
- `node --test scripts/ci/delidev-proto.test.mjs
  scripts/ci/delidev-structure.test.mjs`: all six checks passed.
- `GOMAXPROCS=4 go vet -p 1 ./cmds/delidev-cli/...`: passed.
- Final combined regression run, `GOMAXPROCS=4 go test -race -p 1 -timeout=10m
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker
  ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/subscription
  ./cmds/delidev-cli/internal/cli -run 'Subscription|Managed|Bundle|SessionDeletion|PermanentDeletion'
  -count=1`: all five packages passed on the composed revision. This includes
  the new uncertainty, capability, encoded-remnant and workspace-authority cases
  plus native login/rotation/logout, protected server lifecycle, redaction and
  permanent-session-deletion coexistence.
- `pnpm --filter @delinoio/delidev-api-client typecheck`: passed.
- First final `pnpm --filter @delinoio/delidev-api-client test`: 41 unit tests
  passed, while the integration fixture's existing 120-second Go build deadline
  failed and its three cases were skipped. The command failed. A separate
  `GOMAXPROCS=4 go build -p 1 -o <task-temporary-binary> ./cmds/delidev-cli`
  passed, followed by `GOMAXPROCS=4 pnpm --filter @delinoio/delidev-api-client test`:
  all 44 tests passed across four files. The retry retains the original deadlines.

Required full command `GOMAXPROCS=4 go test -race -p 1 -timeout=20m
./cmds/delidev-cli/...` completed with failure. CLI and workspace each reported
one failing diff case, and the complete server package reached its cumulative
deadline. All other tested packages passed, including the complete Claude,
Codex, Grok, OpenCode, process, storage, bundle and Worker packages. No full-suite
pass is claimed. Earlier full/focused attempts were interrupted
when additional regressions required implementation changes, and the first
protocol check was interrupted to compose newly merged main. Those interrupted
commands are not reported as complete passes.

The full run reported `TestCLISessionAcceptanceQueueAndArchive` failing at
`sessions_test.go:239`: the creation-comparison workspace diff returned
`unavailable`. An isolated current-source retry failed at the same assertion
(43.309-second package). A separate archive of unchanged main
`574c1a92c957fc741a723ff8123888dad32a2194`, containing the original Go module,
DeliDev sources and protocol bindings, reproduced the identical assertion and
classification (40.771-second package) with
`GOMAXPROCS=4 go test -race -p 1 -timeout=5m ./cmds/delidev-cli/internal/cli
-run '^TestCLISessionAcceptanceQueueAndArchive$' -count=1`.
This establishes only that specific pre-existing workspace-diff failure, not a
blanket classification for other full-suite failures. No workspace implementation
or test deadline was changed.

The complete server package reached its cumulative twenty-minute deadline
(1200.793 seconds) while `TestQuestionResponseRejectsChangedExecutionAuthority`
was active for eight seconds and its `thread` subcase was opening a fresh fixture
database. The isolated current-source command
`GOMAXPROCS=4 go test -race -p 1 -timeout=5m ./cmds/delidev-cli/internal/server
-run '^TestQuestionResponseRejectsChangedExecutionAuthority$' -count=1` passed
(27.689-second package). This is a focused retry, not a complete server-package
pass or an unchanged-main classification. Tests after the package deadline are
not assumed to have executed.

The complete workspace package reported
`TestWorkspaceDiffLiteralPathBinaryAndNoExternalHelpers` at `diff_test.go:101`
with `unavailable: The operation timed out` (425.287-second package). Workspace
source is unchanged from main. The isolated current-source command
`GOMAXPROCS=4 go test -race -p 1 -timeout=5m ./cmds/delidev-cli/internal/workspace
-run '^TestWorkspaceDiffLiteralPathBinaryAndNoExternalHelpers$' -count=1` passed
(7.098-second package). No unchanged-main comparison for this additional case or
complete workspace-package pass is assumed.

Both Go embed prerequisites were generated explicitly for formatting hooks.
The temporary repository-owned `dist` outputs were removed after validation;
no generated distribution or binary is included in this change.

## Acceptance limits

No real subscription OAuth, hosted inference, installed-Codex managed execution,
desktop login controls, full uncertain-lease recovery, native Windows/Linux
acceptance or release readiness was performed or established. Complete issue #964
remains independent and unfinished. New-head CI and review after publication are
separate evidence from these local checks.
