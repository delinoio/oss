# Manual PR Git: original local-launcher containment

Source revision: `bf4ce794b4dce7db8754306ff9c811b33f4fa744`, including main
`65eca3341`. Date: 2026-09-30.

## Correction

The first replacement used the generic process runner for local Git inside the
harness launcher. Inspection of the macOS process backend showed that this
runner creates a separate launchd supervisor and coalition; it therefore cannot
establish inherited harness sandbox or original native-owner membership. Earlier
local-command fixtures did not prove that containment assumption.

Local Git now uses ordinary bounded child execution from the sandboxed launcher,
with closed operands, sanitized environment and fixed original Git binary/commit
identity. It retains the original native execution's kernel-owned descendants:
macOS fork/exec coalition inheritance, Linux original subreaper adoption and
Windows original job membership. It does not create a separate supervisor or
temporary ownership journal. The Worker independently closes and joins the
original native owner before validating push, so command exit or an ignored
command error cannot substitute for descendant cleanup.

Native fetch/push still runs only in the authenticated Worker bridge, with exact
workspace, source/ref/head, configuration and one-shot push binding. No native
authentication enters the local child. The workspace contract and scoped Worker
instructions now explicitly prohibit the generic supervisor in this narrow
local-launcher path.

## Executed verification

- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/workspace -run 'PRGitTool|PRLocalGit' -count=1 -timeout=15m`:
  passed (273.668 seconds). This includes bound fork publication, moved-head and
  missing-auth rejection, original scope/one-shot proof, replacement-workspace
  rejection, ignored validation output, default merge/explicit rebase, closed
  local operands and the new original-launcher-parent regression.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...`: passed after this change.
- Regenerated the required embedded assets before the Go commit hook; removed
  those five generated `dist` directories again after compilation/hook completion.
  The final worktree has no repository-owned generated `dist` output. Dependency
  directories and licenses remain intact.

The ancestry regression runs a controlled local executable and asserts its exact
launcher parent. These macOS fixtures and source inspection do not establish
live Codex sandbox denial, real-account fix execution, actual GitHub publication
or every-platform/release acceptance. The original all-package race rerun is
still running with already reported broad fixture failures and intervening main
reconciliation; it cannot establish a full immutable-snapshot pass. Retain the
prior failed frontend/race results and their limits.
