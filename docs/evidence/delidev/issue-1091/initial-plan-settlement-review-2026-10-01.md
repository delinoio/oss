# Initial text-only Grok Plan settlement review

Inspected production source: `642f0161da82af077def33b526414342167733f5`.
Frontend repair base: `1b6b127225571ef6d5dc69aeda465e85a305b9aa`.
Review: https://github.com/delinoio/oss/pull/1230#discussion_r4148427293.

## Assessment

The finding's missing-initialization premise is incorrect. The existing
`runInput` queue-running branch constructs `newPlanningTools` for
`planningInput`, derives native Plan mode from the original mode binding, and
binds its file/question observers before any tool or in-prompt mode event.
The settlement guards therefore have initialized observers for zero-tool Plan.
`input.go` and the production reducers are unchanged by this repair pass.

The new `TestPublicFirstInputInitialPlanSettlesWithoutToolObservations` verifies
that the public input is blocked before mode selection, retains original
creation plus separate claim/bind Plan selection, and then settles a text-only
input with no tool or in-prompt mode observations. It checks original input
claim/bind identity, text/response/end-turn chronology, the independent tools
terminal rather than Execute first-text history, and valid original cleanup.
No fallback, replay, synthetic Plan artifact or additional native authority is
introduced. An English reply is prepared for the original inline review and
will be published only after the final push succeeds.

## Executed verification and qualifications

- The initial isolated positive fixture passed with race detection in 22.458
  seconds, before adding the explicit pre-mode-block assertion.
- The extended public/proposal race selection failed in 129.123 seconds:
  the existing Write and Execute cases and the new Plan case failed during
  native initialization, before the affected prompt settlement.
- The next isolated variant failed in 24.371 seconds during native Plan
  selection. The new test incorrectly shared one 20-second watchdog across
  setup, mode selection and input. It was aligned with the existing public
  fixture's independently bounded setup and separate 20-second input watchdog;
  existing native and production deadlines were not changed.
- Final test source passed:
  `GOCACHE=/tmp/oss-1091-go-cache GOMAXPROCS=2 go test -race -p 1
  -timeout=20m ./cmds/delidev-cli/internal/harness/grok
  -run '^TestPublicFirstInputInitialPlanSettlesWithoutToolObservations$'
  -count=1`: passed in 30.412 seconds.
- `GOCACHE=/tmp/oss-1091-go-cache GOMAXPROCS=2 go vet -p 1
  ./cmds/delidev-cli/...`: passed.

The complete Grok package run started before the final test-watchdog adjustment
and is recorded separately when it completes. No broad passing Go suite, real
hosted-account/platform acceptance or filesystem confinement is inferred from
this focused result. The existing outside-workspace Read decision is pending.
