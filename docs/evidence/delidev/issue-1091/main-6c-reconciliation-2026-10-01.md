# Main Codex Fork reconciliation, 2026-10-01

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). PR:
[#1230](https://github.com/delinoio/oss/pull/1230). This continues the same
one-shot repair begun with the `2026-09-30T14:55:40.466Z` heartbeat; no push
has occurred between the distinct review fixes or main merges.

The final repair inventory still reported the old remote branch as conflicting.
Before pushing, the base read found main
`6c749670727b30679e722821846bc8dc00f5ac32`, adding same-account native Codex
forks through PR #1224 after the previous restore reconciliation. Merge that
base without rebasing. Keep the two Grok review fixes, all earlier evidence,
managed restore and stopped-account-selection behavior.

Eight textual conflicts were ownership/contract additions: desktop, domain,
server, store and Worker AGENTS plus harness, session and protocol contracts.
Both complete feature sections and both sets of scoped rules are retained.
The protocol paragraph preserves stopped-account-switch value 5, restore value 7,
native-accounting value 4 and main's reserved Fork capability value 13; each
implemented capability is advertised independently. No new allocation is invented.
Automatic shared Session/execution merges retain both native Grok reply/mode
presentation and Codex Fork dispatch/publication guards. Fork presentation
requires an original settled Codex source, so Grok cannot gain this profile.

Bindings were regenerated from the combined schemas and matched the merge index.
Protocol lint/breaking and administrator/async-commit-hook embedded builds passed.
API-client tests passed five files and 46 tests. Focused race validation used
private fixtures, `GOCACHE=/tmp/oss-1091-go-cache` and `GOMAXPROCS=4`:

```sh
go test -race -p 2 -timeout=20m ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/grok ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/workspace ./cmds/delidev-cli/internal/cli -run 'Test(PublicProposal|GrokInteractionPreservesExact|GrokPublic|CLIGrok|SessionFork|Fork|BackupRestore.*Fork)'
```

The selection exited 0: domain 1.698 seconds, Grok 6.076, Codex 2.134,
server 82.369, Worker 2.533, workspace 364.423 and CLI 4.314. Store had no
matching selected test; server fixtures exercise fork/restore storage integration.
These results do not stand in for a complete final-source race run.

The required default desktop `pnpm test` passed client build and typecheck but
failed its unit phase: ten files/54 tests failed, while 91 files/1,242 tests passed.
The run had timeouts during high machine load; that observation does not establish
a cause. A complete unchanged-deadline unit rerun with `--maxWorkers=2` passed
100 files/1,295 tests, with one timeout at `settings.test.tsx:723` in the unfiltered
provider-picker fixture. That file was not edited by the Fork merge. Neither run
is claimed as a passing complete default command. The isolated Settings result,
remaining desktop checks and final-source broad Go/protocol checks are recorded
separately once complete. No production or test deadline was weakened.

The earlier restore-base root result remains in
[the integrated repair record](review-repair-validation-2026-10-01.md): 21 packages
passed, two had no tests and one CLI fixture failed then passed unchanged in
isolation. It is evidence for that earlier executable source, not this new base.

The existing outside-workspace automatic Read review still awaits the requested
policy decision. None of these merges or review fixes establishes confinement,
risk acceptance, native account/platform acceptance or merge approval. Normal
hooks run with required generated embeds, and generated repository-owned `dist`
output is removed after final tests/hooks. Current-head CI/reviews await the next
registered heartbeat after the single final push.
