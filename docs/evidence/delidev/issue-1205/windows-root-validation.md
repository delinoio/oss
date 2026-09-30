# Windows OpenCode General Chat root validation

Issue: [#1205](https://github.com/delinoio/oss/issues/1205).
Inspected base: `ad0e3e9a29cb3d8375ab5d168bb160c35a023250` (`origin/main`).
Implementation branch: `kdy1/issue-1205-opencode-windows-roots`.
Validation host: macOS arm64, 2026-09-30. This record describes the change
containing it; native Windows execution was not performed.

## Implemented authority and compatibility

- The accepted Worker manifest and verified lease construct an opaque Git/global
  root. Windows global native metadata remains exact `/`; canonical filesystem
  ownership is independently derived from the actual workspace's local drive.
- Initialization and the existing instruction/configuration rechecks inspect
  canonical roots, every enclosing `.git` entry and prohibited configuration
  sources. Global additive instructions remain session-only. No native response
  supplies filesystem authority, and unsupported UNC/device/relative aliases
  remain refused.
- Native path and primary Build/Plan inventories retain exact comparisons.
  Windows Plan's relative exception uses the original native process/runtime
  cwd drive, including a different workspace drive. The fresh environment
  excludes per-drive cwd entries; server/renderer cwd is not an operand.
- New Windows global native checkpoints use private version 2 with both roots.
  First dispatch, FIFO, explicit Resume and read-only completed-report recovery
  compare independent manifest roots with the original checkpoint. Existing
  account/model, original receipts/history, process/workspace ownership and
  cleanup gates remain required. No input is replayed.
- Version 1 remains the Unix/Git byte format and validator. Windows global
  replacement cannot promote a version-1 envelope or infer its missing boundary.
  Public schemas, generated bindings and CLI commands are unchanged.

## Source verification

The pinned upstream revision is
`545f51d26cc39a907d2867492d498d9607ea5fa4` (OpenCode `1.18.32`).
Its [project implementation](https://github.com/anomalyco/opencode/blob/545f51d26cc39a907d2867492d498d9607ea5fa4/packages/opencode/src/project/project.ts)
returns literal `/` for a global non-VCS worktree. Its
[agent implementation](https://github.com/anomalyco/opencode/blob/545f51d26cc39a907d2867492d498d9607ea5fa4/packages/opencode/src/agent/agent.ts)
computes Plan's exception with native `path.relative`.
[Node's path contract](https://nodejs.org/api/path.html) documents Windows
resolution and per-drive working-directory behavior. Matching Bun/OpenCode
execution on Windows still requires the installed native fixture below.

## Executed checks

| Check | Actual result |
| --- | --- |
| Focused deterministic global-root and checkpoint tests | Passed on macOS. Covers distinct authorities, exact `/path` and Build/Plan inventories, late `.git`/configuration sources without mutation, and uncertainty latching. |
| `go test ./cmds/delidev-cli/internal/server -run TestOpenCodeFirstDispatch -count=1` | Passed; exercises the existing public dispatch prerequisites after removal of the OS-only exclusion. |
| `go vet ./cmds/delidev-cli/...` and serial `go vet -p 1 ./cmds/delidev-cli/...` | Passed. |
| `GOOS=windows GOARCH=amd64 go test -c` for OpenCode and server packages | Passed. This checks compilation only. |
| `pnpm --filter @delinoio/delidev-api-client test` | Passed: 4 files, 44 tests. |
| `pnpm --filter @delinoio/delidev-api-client typecheck` | Passed. |
| `buf lint` | Passed. |
| `pnpm proto:lint` | Failed at the existing `buf format --diff --exit-code` check: missing blank lines in `protos/delidev/v1/activity.proto` around lines 52 and 97. The file is byte-unchanged from the inspected base. |
| Broad `go test -race ./cmds/delidev-cli/...` | Failed. Multiple CLI/native-harness/server/Worker tests hit initialization or suite timeouts during concurrent local test load. The race detector also reported an unsynchronized `bytes.Buffer` read in unchanged Grok `api_test.go` versus process logging. This is not a passing broad-suite result or proof that all failures are environmental. |
| `go test -race -p 1 ./cmds/delidev-cli/internal/harness/opencode ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/server -run 'OpenCode\|GlobalRoot' -count=1 -timeout=20m` | Passed: OpenCode 26.273s, Worker 361.126s, server 180.096s. Installed native tests remain opt-in/skipped. |
| Serial full OpenCode `go test -race -p 1 ./cmds/delidev-cli/internal/harness/opencode -count=1 -timeout=15m` | Failed in unchanged `TestProbeOwnsAuthenticatedServerAndCleanup` trailing/warning/stderr cases (cleanup or initialization classification under local load). No other test failure or race report appeared in that run. |
| `go fmt ./...` and `git diff --check` | Passed. |

Two fixed synthetic original checkpoints were serialized with the inspected
base's version-1 struct and checked against current serialization before fixing
their bytes. Their canonical round-trip/digest regression test passed on macOS:

| Fixture | SHA-256 |
| --- | --- |
| `checkpoint-v1-unix.json` | `33592edc05ef46941a4d949fc101b123c2e63c761634c352249a89b929ddf017` |
| `checkpoint-v1-windows.json` | `075288e2b43a508d8f9f65ca30deb4efac28d82b8e40811994c7826bbe8316b5` |

Paths in those fixtures are fictitious. Platform-specific decoding runs on the
matching OS; macOS execution does not establish Windows decoding acceptance.

## Installed Windows fixture and remaining evidence

`TestManualNativeWindowsOpenCodeGeneralChatLifecycle` is an opt-in Windows
scripted-provider fixture for Build/Plan FIFO, text Stop/Resume, and lost-report
recovery for first/resumed/switched/failed/missing-checkpoint scenarios. It reuses
the public owner/controller/Worker lifecycle and verifies provider calls,
original native identity/history and completed cleanup. Set
`DELIDEV_NATIVE_OPENCODE_EXECUTABLE` to an already installed pinned Windows
executable; the fixture does not install or upgrade it or use real credentials.

`TestManualNativeWindowsOpenCodeDifferentDrivePlan` also requires
`DELIDEV_NATIVE_OPENCODE_WINDOWS_WORKSPACE_PARENT`, an independently selected
canonical writable parent on another local drive. The fixture owns only its
new temporary child and removes it during cleanup.

The Windows unit tests cover original-process Plan semantics and permission
ordering, global-project identity, unsupported path aliases, both checkpoint
root corruptions and legacy-promotion refusal. Neither those unexecuted Windows
tests nor cross-compilation establish actual installed Windows Build/Plan,
FIFO/Stop/Resume/recovery, cross-drive, account or platform acceptance. Those
native acceptance results remain required before claiming issue #1205's full
real-environment completion. The local installed OpenCode is `1.1.53`, so it was
not used as evidence for the pinned `1.18.32` profile.
