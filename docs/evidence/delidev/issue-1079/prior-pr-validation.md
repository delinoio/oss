# Historical PR #1121 evidence

The following source-backed validation records are preserved from closed, unmerged PR #1121 at `fb1a31e43cc8a87cce163ade660f960b96baa933`. They describe those prior revisions and are not validation of this replacement branch.

## Issue #1079 — Worker-local workspace snapshots and manual storage

Implemented owner/paired-client Connect and equivalent CLI preview, create,
cleanup, inspection, restore, deletion, cancellation and explicit recovery.
Acceptance retains actor-bound UUID-v7 receipts, exact revisions, original job
references, atomic metadata/session/events and paused dispatch. Existing schema
and historical records remain unchanged. Worker bytes remain private and local.

The snapshot engine uses one manifest and independent Git stores for every
ordered repository or General Chat, preserving staged/unstaged/untracked/ignored,
unpushed, executable-mode and symlink data. Whole source observation, complete
published copies and Git integrity precede a no-replace source-root transition.
Removal intents and explicit journal-bound recovery preserve uncertainty instead
of replaying native publication. Active ownership, original Local checkouts,
unresolved dependents and occupied restoration destinations are protected.
Logical source/retained snapshot/removal counts remain separate from measured
filesystem free space. No remote push or automatic retention is introduced.

Focused General Chat, interrupted removal/restore/delete, foreign retained files,
real Unix socket/FIFO/device, server receipt/Resume/only-copy protections and
Worker no-replay/assignment tests pass in temporary state. The focused race run
for two-repository restoration, preserved execution-lease identity, unchanged
local test remotes, second-repository disk failure and mid-copy cancellation
passes (231.862s; `/tmp/delidev-1079-snapshots.log`). Complete Go vet passes.
The API client passes 3 files / 41 tests, type checking and build. Root
`pnpm proto:check` passes formatting/lint, origin/main compatibility and generated
freshness (`/tmp/delidev-1079-proto.log`). Linux arm64 and Windows amd64 workspace
test binaries cross-compile; cross-compilation does not prove native runtime.

The complete `go test -race -timeout=30m ./cmds/delidev-cli/...` run finished
with failures. Its existing
`TestCLISessionAcceptanceQueueAndArchive` hits the bounded Worker file-reader
comparison deadline (`/tmp/delidev-1079-race.log`). An isolated repeat and the
serialized final run hit the same existing boundary. A separately archived source
checkout of fetched main `b741cec88d68ba84eaf918bbee22ca28bff57ec6` reproduces
that fixture failure on its review-create file comparison (56.75s;
`/tmp/delidev-1079-baseline-cli.log`), without these changes. The production
read deadline is retained. The complete store and Worker race suites pass
(1201.411s and 1252.380s). Existing Grok and workspace native ownership/read
fixtures also hit bounded deadlines; server and workspace suites exhaust the
extended 30-minute package budget while running their existing OpenCode shell
publication and workspace-read cases. These are failed checks, not whole-tree
acceptance. A second serialized attempt reproduced the CLI boundary and was
stopped after the complete first result; it does not supply a passing full result.
See `/tmp/delidev-1079-race.log` and `/tmp/delidev-1079-race-final.log`.

The typed second-repository disk/quota exhaustion regression passes (54.856s;
`/tmp/delidev-1079-capacity.log`), preserving both sources. Cancellation after
verified snapshot publication and before source removal now retains
recovery-required ownership instead of orphaning its server metadata. Explicit
recovery registers that same snapshot, preserves the present source, settles the
incomplete cleanup as failed and stays paused. Its focused race regression and
original recovery checks pass (3.623s;
`/tmp/delidev-1079-publication-cancellation.log`).

PR #1121's protocol/client CI exposed the existing real-server GitHub-profile
Settings fixture's one-second DOM wait. The unchanged full Settings suite
reproduced its missing freshly saved profile, while the isolated case passed.
Giving both durable-save/list observations the existing 15-second fixture I/O
budget passes all 11 Settings cases without changing production RPC deadlines or
assertions. Required desktop `pnpm test` passes type checking, 74 files / 941
React tests, eight packaging checks, six launcher checks and frontend build
(`/tmp/delidev-1121-desktop-test.log`). Go vet passes again after the cancellation
repair (`/tmp/delidev-1079-vet-repair.log`). Generated output is removed after
validation. Codex review on the original PR head is unavailable because its
review usage limit was reached; that comment is not review approval.

These fixtures use actual local Git and private temporary state with controlled
storage faults, no user credentials or native AI inference. They do not establish
native Windows/Linux runtime acceptance, real provider/private-GitHub access,
physical reclaimed-space attribution, a desktop storage-management surface,
permanent session/dependent Sidechat deletion or database restoration.

### PR #1121 Windows snapshot CI repair (2026-09-30)

Windows Go CI at `ba87a337` failed three new Git-backed snapshot cases after
successful previews. Their default runner temporary roots put copied loose Git
objects beyond 260 characters. Offline commands discard system/global settings;
[Git for Windows disables long paths by default](https://gitforwindows.org/git-cannot-create-a-file-or-directory-with-a-long-path.html). Snapshot checks now enable
`core.longpaths` per Windows command while preserving source configuration and
all offline/identity checks. Closed commit/object/location failure phases improve
redacted diagnostics. The new independent-copy regression uses a Git directory
longer than 280 characters, retains staged content and validates after taking the
original repository offline. It passes on macOS arm64 with the race detector
(5.394s; `/tmp/delidev-1121-long-path.log`). Native Windows revalidation remains
pending; a local macOS pass is not Windows acceptance.

The copied-config durability step also opened files read-only before calling
`Sync`, while [Windows `FlushFileBuffers` requires write access](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-flushfilebuffers). It now opens
only copied configuration with `O_RDWR`, retains flush failures and checks close
errors. The long-path, faithful two-repository restoration and stale-preview/
destination-conflict race regressions pass locally (40.953s;
`/tmp/delidev-1121-config-flush.log`). This platform API correction still requires
the next native Windows CI run.

The disk-full fixture now injects native Windows `ERROR_DISK_FULL` or Unix
`ENOSPC` on the matching platform. Both second-repository fault cases require
that repository to be reached, so an unrelated earlier copy failure cannot
satisfy the preservation/cancellation assertions. Failed Git-backed fixtures
print retained structured diagnostics. Disk-full and mid-copy cancellation race
checks pass on macOS arm64 (15.555s;
`/tmp/delidev-1121-native-faults.log`); the final Windows amd64 workspace test
binary cross-compiles (`/tmp/delidev-1121-windows-cross.log`).

The final long-path fixture places loose objects beyond 280 characters while
keeping the subprocess working directory at 230–231 characters; Windows
[limits process creation from an oversized working directory](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-setcurrentdirectory).
This matches the failing staging layout without requiring a separate process
launch capability. The final focused race run passes (6.547s;
`/tmp/delidev-1121-final-long-path.log`), and Windows test cross-compilation passes
again after this fixture adjustment.

Final validation: `go vet ./cmds/delidev-cli/...` passes
(`/tmp/delidev-1121-vet.log`). The complete `go test -race -p 2
./cmds/delidev-cli/...` run passes every package except Grok, which exhausts its
default ten-minute total package budget while its active question-claim subcase
has run only two seconds. CLI (147.478s), server (352.236s), store (83.871s),
Worker (135.701s) and the full workspace suite (548.039s) pass
(`/tmp/delidev-1121-full-race.log`). An isolated
`go test -race -timeout=30m ./cmds/delidev-cli/internal/harness/grok` passes
(945.424s; `/tmp/delidev-1121-grok-race-extended.log`). Every DeliDev package
therefore has a passing race result, while the original default-budget command
remains recorded as failed. Production operation/read deadlines are unchanged.

### PR #1121 base-conflict reconciliation (2026-09-30)

Merged main `60770d0649622739f5035b3abbfb30bd9b81331e` into the existing
workspace-snapshot branch. The provider inventory cursor policy and complete
widget/profile evidence additions are retained alongside all snapshot contracts
and earlier Windows repairs. The profile fixture retains main's sequential
600 ms save/list delays and this branch's bounded 15-second durable-I/O waits,
with the unchanged 30-second scenario budget and exact revision-2 CLI assertion.
The focused real-server save/rename regression passes; full desktop `pnpm test`
on Node.js 24 passes 74 files / 945 tests, type checking, eight packaging tests,
six launcher tests, native Swift widget fixtures and the production build
(`/tmp/delidev-1121-merge-front.log`). Go vet, all 95 repository contract checks
and workflow validation pass.

The prior snapshot-repair head `2804517e60abb676add674b7f66536dbd164f74b`
passed native Windows Go CI in
[run 36639901384](https://github.com/delinoio/oss/actions/runs/36639901384),
with all ten active PR checks successful. That result belongs to the prior head;
the merged head requires fresh CI. No Codex approval is claimed while its
review integration reports a usage limit.

Root `cargo test -- --test-threads=1` passes every workspace unit, integration
and doctest target with canonical `TMPDIR=/private/tmp` and the existing CEF
cache (`/tmp/delidev-1121-merge-cargo-prepared.log`). The initial run stopped at
two unchanged pnport supervisor tests because the newly restored checkout lacked
its required native injection library (`/tmp/delidev-1121-merge-cargo.log`).
The documented macOS preparation command, `cargo build -p pnport
-p fspy_preload_unix --features fspy_preload_unix/pnport`, built that library;
the full root command then passed without source changes. LFS source assets were
hydrated before consumption, and generated desktop/client `dist` directories
were removed after verification.

The complete `go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...`
command passes every package (`/tmp/delidev-1121-merge-go-race.log`), including
CLI (211.986s), Grok (876.945s), server (387.598s), store (108.474s), Worker
(150.233s) and workspace (401.917s). The longer total package budget accommodates
the existing complete Grok suite without altering production deadlines.

### PR #1121 forwarding-protocol reconciliation (2026-09-30)

Merged main `70ec8ab59dd35931cde38b271e29583e8cfcb62a` while retaining both
forwarding and workspace-storage services, contracts, generated query exports
and independent cleanup rules. Main already owns system capability value 2 for
`SESSION_FORWARDING_V1`; the unmerged workspace-storage extension now uses the
unused value 3. Go/TypeScript/Connect bindings are regenerated from the combined
schema, and real authenticated GetStatus must advertise both named capabilities.
Existing forwarding capability values and RPC identities are unchanged.

The focused server race check passes (5.218s), covering authenticated status,
workspace-storage receipts/Resume/only-copy protections, malformed report and
queued cancellation, exact forwarding bytes/Stop-versus-Archive, and receipt-only
offline cleanup (`/tmp/delidev-1121-repair-0134-focused.log`). Buf formatting,
lint and compatibility against main pass. The generated API client passes
3 files / 41 tests and type checking. The profile test retains the shared
600 ms latency fixture and 15-second waits, adopting main's explanatory comment.

Required root `pnpm proto:check` passes formatting/lint, compatibility against
main and complete Go/TypeScript/Connect Query regeneration without drift
(`/tmp/delidev-1121-repair-0134-proto.log`). The full desktop `pnpm test` on
Node.js 24 passes 74 files / 945 tests, type checking, eight packaging tests,
six launcher tests, native Swift fixtures and production build
(`/tmp/delidev-1121-repair-0134-front.log`). Go vet passes. Generated desktop
and API-client `dist` directories are removed after verification.

The aggregate race command first failed `TestCLISessionAcceptanceQueueAndArchive`
in the unchanged main CLI fixture: the second repository's stale-allowed review
submission returned the typed unavailable file-reader error while native diff
observations approached the existing 15-second read deadline
(`/tmp/delidev-1121-repair-0134-race.log`). This failure remains recorded; the
aggregate command is not a pass. The same case then passes in isolation
(98.463s), and the complete CLI package passes with the race detector (174.868s)
without source changes (`/tmp/delidev-1121-repair-0134-cli-isolated.log` and
`/tmp/delidev-1121-repair-0134-cli-race.log`). Production deadlines and coverage
are unchanged. This outcome matches the previously documented file-read deadline
failure, but a successful rerun alone does not prove its cause.

The completed `go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...`
run passes every other package, including forwarding (1.867s), Grok (927.819s),
server (631.388s), store (194.747s), Worker (363.611s) and workspace (549.915s).
Its exit status is 1 solely for the recorded CLI fixture failure; combined with
the passing full CLI rerun, every DeliDev package has passing race coverage
across these runs. This is not a passing aggregate command. The 30-minute
per-package test budget preserves existing production deadlines. No Rust source
changed in this reconciliation; the preceding merge's complete root Cargo
validation remains recorded above.

### PR #1121 icon/dependency reconciliation (2026-09-30)

Merged main `da93cb9b9962acb100cb018e255e4ff0b2374e33`, retaining both complete
workspace-snapshot repair evidence and main's desktop source-icon preparation
evidence. The combined project contract preserves storage ownership and the
new icon preflight. Main's source icon, scoped hydration implementation and
security dependency resolutions are retained. Conflict verification confirms
both original evidence blocks are present and no markers or whitespace errors
remain. Go, protocol and Rust source bytes are unchanged from the preceding
repair head `8852a87ab2b4535bd4387f4c9e7bbd5812a43cbd`.

### PR #1121 singleton-preferences CI fixture repair (2026-09-30)

The `DevHud Protocol and Client` job at run `36659300077`, job
`109710350518`, passed protocol lint/breaking/freshness and both API-client
suites, then failed only the singleton-preferences Settings case (944/945
frontend tests passed). Its first post-save `Edit Server preferences` lookup
used the DOM library's default one-second limit despite sequential durable save
and inventory refresh RPCs.

The retained real-server fixture now delays each selected SETTINGS
SaveConfiguration/ListResources reply by 600 ms. With the old one-second lookup,
this reproduces the same failure deterministically
(`/tmp/delidev-1121-repair-0221-settings-negative-prepared.log`). Explicit bounded
five-second New/Edit waits then pass all 11 Settings integration cases (18.57s),
with the overall 15-second singleton budget, exact Go defaults, original ID,
changed fields and revision increment unchanged
(`/tmp/delidev-1121-repair-0221-settings-positive.log`). Production code and RPC
deadlines are unchanged.

An initial focused invocation could not load the removed generated client and
ran no tests; rebuilding the client supplied that prerequisite before the
negative/positive comparison. Main's source icon was restored from its local
LFS cache by the shared asset preflight, and root frozen installation passed
with the retained security dependency resolutions.

Final local validation passes the complete Node.js 24 desktop `pnpm test`:
74 files / 945 React tests, generated-client build/type checking, eight packaging
checks, 16 asset/launcher cases, native Swift widget fixtures and production
build (`/tmp/delidev-1121-repair-0221-front.log`). Root `pnpm proto:check`
passes lint, main compatibility and complete generation freshness. The API
client passes 41 tests and type checking; all 95 root contract cases and workflow
validation pass. Generated desktop/client `dist` output is removed. Unchanged
Go/Rust sources retain the preceding repair's explicitly recorded validation,
including the aggregate CLI deadline failure and passing CLI rerun; those
results are not relabeled as a new aggregate pass.

### PR #1121 permission-guidance reconciliation (2026-09-30)

Merged main `66206911e1bf901032de9d0cac373eefaae9ab39`, preserving both
permission-guidance and singleton-preferences fixture policies, the complete
project storage contract and every original evidence addition from both sides.
The permission-denial source and regressions remain as merged on main; workspace
storage and the delayed singleton save/inventory regression remain intact.
Focused desktop, local-registration and Settings integration checks pass
3 files / 27 tests (`/tmp/delidev-1121-repair-0239-focused.log`). Go, Rust and
protocol sources are unchanged from the preceding repair. The new native
permission guidance does not imply additional platform/release acceptance.

The complete Node.js 24 desktop `pnpm test` passes 74 files / 948 React tests,
generated-client build/type checking, eight packaging checks, 16 asset/launcher
cases, native Swift widget fixtures and production build
(`/tmp/delidev-1121-repair-0239-front.log`). All 95 repository contract cases
pass. Earlier protocol/API-client/workflow and Go/Rust validation remain
explicitly recorded for their unchanged sources; this merge does not relabel
historical failed commands as passes or add native application acceptance.
Generated desktop/client `dist` directories are removed after verification.

### PR #1121 current-user services merge repair (2026-09-30)

- Merged main revision `545b4080` (current-user services, PR #1123) into the issue #1079 workspace-storage branch. Preserved both public service surfaces, scoped rules, contracts, and every conflicting evidence block. Main's existing capability wire values remain automatic titles `1`, forwarding `2`, and user services `3`; the unmerged workspace-storage capability moves to `4`. The server advertises all four independently, and Go/TypeScript/Connect Query outputs are regenerated from the canonical schema. Updated scoped protocol rules and the contract in the same repair.
- Focused real authenticated Connect authorization, metadata, revision/replay and redaction fixtures pass under the race detector (server package, 3.304 seconds). API-client lint, all 42 tests (31.57 seconds), and build pass in the initial Node 26 invocation; canonical Node 24 results follow below. Its real temporary Go-server fixture observes all four independent capabilities without creating a native service registration. Protocol formatting/lint and main breaking checks pass. Full package race/vet and committed generated-freshness validation follow this local merge commit; those results are recorded separately below. No native service registration, user credentials, inference or publication was used.

- Complete merged-tree validation passes: root `go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...` passes every package (22 tested packages plus two without tests), including CLI 188.037 s, Grok 903.133 s, server 575.055 s, store 108.031 s, user-service controller 4.612 s, Worker 267.474 s and workspace 507.106 s. Root `go vet ./cmds/delidev-cli/...` passes. This aggregate command succeeds without a fixture retry; historical failures from preceding repairs remain recorded above.
- Canonical Node 24 validation passes API-client lint, all 42 tests (2.03 s), and build; root `pnpm proto:check` passes formatting/lint, main breaking compatibility and byte-for-byte generation from the committed merged schema; all 95 `pnpm ci:contracts` checks pass. Full desktop package-local `pnpm test` passes 74 files / 948 tests, type checking, eight packaging checks, 16 asset/launcher cases, native Swift widget fixtures and production build. The preceding Node 26 desktop run also passed, but Node 24 supplies the supported-runtime evidence.
- Complete CLI Windows and Linux amd64/arm64 cross-builds pass with CGO disabled. These prove compilation, not native platform execution or real service-manager acceptance. Both owned frontend/client `dist` directories and temporary cross-build binaries are removed. No Rust source changes; earlier root Rust evidence remains historical. New-head hosted CI and code-review approval still require independent evidence after the push.


### PR #1121 icon and Settings-label reconciliation (2026-09-30)

- Merged main revision `1399d131` (the approved icon enlargement, PR #1133, and shortened Settings label, PR #1139) into issue #1079. Retained both complete sides of the evidence-ledger conflict, all main contracts/scoped rules, the enlarged icon/export bytes and the shared `AI Subscription` label with its 16-category identity/navigation assertions. Existing snapshot, forwarding, user-service and delayed real-server fixture policies remain intact.
- Explicit desktop asset preparation confirms the updated exact-path LFS icon is hydrated and ready; `git lfs fsck` passes. The typed client prerequisite builds on Node 24, and focused Settings component tests pass (one file / 23 tests, 2.55 seconds). Full frontend/contract validation follows this local merge commit and is recorded below.
- Go, Rust, protocol, shared-package, Cargo and Go dependency source bytes are unchanged from tested head `37c62f87`; its successful complete DeliDev race/vet, generated-protocol and API-client checks remain recorded above rather than being rerun for these frontend/assets/docs changes. No new native desktop, platform service-manager, inference or release acceptance is claimed by this merge.

- Complete Node 24 frontend validation passes package-local `pnpm test`: 74 files / 948 tests, TypeScript checking, eight packaging checks, 16 asset/launcher cases, native Swift widget fixtures and production build. All 95 root `pnpm ci:contracts` checks pass. Exported icon files match the merged main revision, and both owned frontend/client `dist` directories are removed. No Go/Rust/protocol sources changed from `37c62f87`; their preceding executed validation remains separately recorded. Fresh CI and review evidence are required after this push.
