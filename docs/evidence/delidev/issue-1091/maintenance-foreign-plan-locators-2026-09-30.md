# Public Grok foreign Plan locator repair, 2026-09-30

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). Replacement PR:
[#1230](https://github.com/delinoio/oss/pull/1230). This independent record
supplements the earlier replacement and main-reconciliation evidence without
changing their historical results.

The first maintenance pass merged main
`d1f83cecee4e0c50ea094335392cf68845f85739` as
`088ccf702c65c4537cf3d87baa094e0b5a16e97e`. Four appended instruction/contract
conflicts retained both the original Grok requirements and main's Windows
OpenCode General Chat root profile. The merge does not remove the separate
Grok repository, continuation or native-profile gates. Protocol freshness and
all three protocol allocation/compatibility regressions passed.

The original PR head's [Windows server CI job](https://github.com/delinoio/oss/actions/runs/36710716288/job/109871649040)
failed all four `TestGrokPublicOriginalPlanQuestionsRevisionsAndTransitions`
outcomes at the original Plan entry publication. Its pure server reducer used
the server host's `filepath.IsAbs`/`filepath.Clean` rules for a retained POSIX
locator. Those rules also incorrectly interpret Windows locators on Unix
servers. This is a real cross-platform publication bug, independent of native
filesystem ownership.

The repair gives the pure public reducer an explicit retained-locator policy:
bounded nonempty NUL-free UTF-8, at most 8,192 bytes, and unchanged exact identity
across entry, Plan-file Writes and exit. It performs no filesystem
interpretation or normalization. The original Worker observer retains its
separate native-path policy and exact locally owned absolute normalized path.
No public locator grants filesystem access, mode selection, native response
authority, continuation or replacement execution.

Controlled local checks used the task-private Go cache and scripted original
native fixtures, without user credentials or selected hosted accounts:

| Check | Result |
| --- | --- |
| `go test -race -p 1 ./cmds/delidev-cli/internal/harness/grok -run 'TestPublicToolJournal\|TestOriginalPlan\|TestOriginalPlanning' -count=1` | Passed in 91.029 seconds, including the original private Plan ownership/schema/response tests. |
| Final `go test -race -p 1 ./cmds/delidev-cli/internal/harness/grok -run 'TestPublicToolJournal' -count=1` | Passed in 3.076 seconds. POSIX, Windows drive and UNC locators each retain approved, cancelled, abandoned and revised lifecycles through both observed and prebound public journals. Changed exit locators are rejected without altering original evidence; oversized, NUL-containing and invalid UTF-8 constructor locators are rejected. |
| `go test -race -p 1 ./cmds/delidev-cli/internal/server -run 'TestGrokPublic\|TestGrokInitialPlan' -count=1` | Passed in 29.561 seconds, including the four CI-failing Plan outcomes and public Write/question/response acceptance. |
| Root `go vet -p 2 ./cmds/delidev-cli/...` | Passed after the path-policy repair. |
| Required root-hook embedded administrator and async-commit-hook asset builds | Passed; generated output is not tracked. |

These local portable regressions do not establish an executed Windows native
controller or hosted-account result. The repair still requires new-head CI;
the earlier Windows failure remains visible until that validation completes.
The prior full frontend-script and broad Go-suite limitations remain in their
independent records. No source fixture deadline was increased, and no release
or full issue #964 acceptance is claimed.
