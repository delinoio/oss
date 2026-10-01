# Main merge and local validation limits

## Combined source

Repair PR #1216 merges main `574c1a92c957fc741a723ff8123888dad32a2194` into
`c1f4de21b4cb30f7c59beb22c202c990d3e7a4a5`. Independent network and native-accounting
rules are composed in scoped instructions, contracts and status. Capability 6
remains separate from native accounting 4 and permanent deletion 9. All generated
bindings are regenerated from reconciled source. Main's marked schema 25 remains
intact; network profiles/routes reuse its existing entity/event/receipt tables
without another migration. The network storage wording no longer fixes a historical
schema version. Initial implementation evidence retains its original schema-24 base.

## Completed combined-tree checks

- Focused race tests across server, CLI, domain, outbound, provider, inference and
  GitHub packages passed with `GOMAXPROCS=2`, `-p 2`, `-count=1`, `-timeout 5m`
  and filter `Network|CLINetwork|NativeAccounting|GrokClosed|GrokAccounting|AccountingProfile`.
  The store package had no matching tests in this filter; that is not a store-suite pass.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...`: passed.
- API client `pnpm typecheck && pnpm test`: passed all 45 tests.
- `pnpm proto:generate`, `pnpm proto:lint && pnpm proto:breaking`: passed.
- The three structure/protocol/breaking CI fixture files passed all seven tests.
- Combined-tree desktop `pnpm test` with a temporary two-worker cap completed
  with 1,238 passing and five failing tests across four failing files. Its config
  was restored on exit. This does not establish a green complete frontend command.

Full merged-tree Go race results, focused frontend retries and post-commit protocol
freshness are pending at this merge commit and will be recorded separately.

## Previous-head validation and comparison

At `c1f4de21`, GitHub CI run 36700558670 completed all selected checks successfully,
including Linux/macOS/Windows Go, Go quality, protocol/client and CI contracts.
That result does not certify this merge's new head or establish Codex approval.

The earlier full local frontend run completed with 1,209 passing and 27 failing
tests. All its failing App/Settings tests subsequently passed in isolated
one-worker runs (44 App tests; 49 Settings plus five desktop tests). The bundle
fixtures passed eight tests, desktop-launch/asset fixtures passed 16 tests,
Widget fixtures passed and the production build passed. Generated dist outputs
were removed after consumption.

The earlier serial full Go attempt failed `TestCLISessionAcceptanceQueueAndArchive`
at sessions_test.go:202 with a timed-out `session create --wait`; the same assertion
and symptom reproduced on an untouched archive of main `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`
in 89.54 seconds. The branch's full CLI package took 548.679 seconds and failed that
case in 78.19 seconds. This comparison establishes the unchanged-main timeout
symptom, not identical causes for every native fixture failure. The serial attempt
also reported Claude request-delivery, checkpoint and probe-cleanup failures,
then was interrupted with its owned children for a parallel retry. That retry was
superseded by this required main conflict repair; neither interrupted attempt is a
complete combined-tree race result.

All fixtures use temporary state and test secrets. Shared-machine contention was
observed during the failing runs. No assertions or operation time bounds were
relaxed to obtain passing results. Real accounts, enterprise networks, native
credential lifecycle, platform/release and Worker bootstrap acceptance remain
unperformed.
