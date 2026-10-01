# Original Git environment casing

The eighth repair pass evaluated Codex thread `PRRT_kwDORRAKg86nveBX` against
PR #1227 at `caaa5dd44a8123c50220bcac69824298cf3b1e42`. The receive-pack
preflight preserves the spelling returned by `gitEnvironment()`, while the
bridge snapshot previously uppercased every name. On POSIX, that promoted inert
lowercase aliases into active Git/SSH or PATH settings. The finding is valid.

The bridge snapshot now preserves exact names on POSIX and normalizes names only
for Windows. It retains the last applicable value and deterministic sorted
digest order. Values remain private; the repair adds no environment logging,
credential authority, new transport or permission profile. This does not repair
the original executable/configuration verification-to-execution security P1 or
the separate companion-content P1.

Executed verification on the working repair:

- The native POSIX snapshot regression failed against the prior implementation
  because the bridge changed a preflight entry. Log:
  `/tmp/delidev-1227-eighth-env-red.log`.
- `GOMAXPROCS=2 go test -race -v -p 1 ./cmds/delidev-cli/internal/workspace -run
  '^TestOriginalPRGitEnvironment|^TestPRGitEnvironment' -count=1 -timeout=2m`
  passed (package 2.019 seconds). Coverage includes distinct POSIX aliases, last
  exact-name values, modeled Windows case-insensitive last values, stable
  resnapshotting and the actual native POSIX preflight/bridge environment.
  Log: `/tmp/delidev-1227-eighth-env-unit.log`. Windows is a unit-model result,
  not native Windows acceptance.
- The broader race selection of PR Git tool, push, local and bridge cases failed
  (package 283.034 seconds). `TestPRGitBridgeRetainsCommandOwnershipThroughLocalChild`
  and `TestPRGitToolForkPushIsOriginalBoundAndNeverReplayed` failed fixture
  preparation with unconfirmed owned descendant cleanup. The merge/rebase
  conflict fixture failed cleanup and a bounded operation deadline. These
  preparation points precede original Git-tool snapshot creation by code
  inspection; their failures remain recorded without a baseline or resource-cause
  dismissal. Log: `/tmp/delidev-1227-eighth-env-focused.log`.
- Workspace package vet and CLI build passed. Required DevHud administrator and
  async-hook embed prerequisites were generated from their owning packages.
  Logs: `/tmp/delidev-1227-eighth-go-vet.log`,
  `/tmp/delidev-1227-eighth-go-build.log`, and
  `/tmp/delidev-1227-eighth-embed.log`.

The owning workspace contract's complete package race result is recorded in the
separate final validation record. No full-suite, live-account, native-platform
or release acceptance is established here. The corrected thread remains open
until the single final repair push succeeds; both original security decisions
remain pending.
