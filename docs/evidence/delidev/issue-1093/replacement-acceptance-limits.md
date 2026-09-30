# Replacement validation and acceptance limits

Recorded on 2026-09-30 on macOS arm64. Implementation checkpoint: `8817d892`;
the companion actor-receipt repair is verified in
[its regression record](actor-receipt-regression.md).

`DEVHUD_PROTO_BASELINE=ad0e3e9a29cb3d8375ab5d168bb160c35a023250
pnpm proto:check` passed, including full lint/breaking checks and regenerated-source
freshness. The generated clients passed typecheck and all 44 tests. Required
administrator and ach embed bundles were explicitly generated for the root Go
formatting hook and remain ignored output. No frontend or Rust implementation
change was introduced.

## Actual pinned native attempt

Used the official disposable npm `@anthropic-ai/claude-code-darwin-arm64@2.1.236`
artifact, isolated server/Worker/native runtimes and a scripted loopback provider.
The initial attempt selected the binary through macOS's `/tmp` alias and failed
the existing canonical executable-identity gate. The opt-in fixture now resolves
that selection before constructing its synthetic discovered installation; the
production ownership gate is unchanged.

The canonical-path `TestManualNativePublicSessionCompaction` run failed all four
scenarios (373.558 s package run): success timed out while observing the first
execution; provider rejection, insufficient history and cancellation retained
initial-execution recovery-required outcomes. None reached public manual-compaction
acceptance, so this run proves neither command outcomes nor checkpoint replacement.

The unchanged private baseline
`TestManualNativeExplicitCompaction/successful/continue-false` also failed in
`OpenAPISession`, before native compaction, with request-delivery uncertainty
(23.253 s package run). These failures do not establish their cause. Production
initialization/cleanup bounds and fixture ownership requirements were preserved.
No user credentials, hosted account, subscription, other-platform or release
acceptance is claimed.

## Aggregate race suite

`GOMAXPROCS=2 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...`
was launched before the actor-receipt repair. At this checkpoint it has reported
failures in `TestCLISessionAcceptanceQueueAndArchive`,
`TestDiscoveryVerifiesGrokWithoutExecution`,
`TestDiscoveryVerifiesOpenCodeWithoutExecution`, and
`TestDiscoveryBoundsAndGrokUpdateSuppression`. Connections, credentials, domain,
forwarding and API proxy packages completed successfully. Remaining package
execution is still in progress; this is not a complete or passing suite result.
Concurrent local test runs were observed, but that is not proof of the failures'
cause. Final collection belongs in a separate issue evidence file.

Focused compaction, cleanup/deletion and authenticated receipt race checks passed
as recorded above and in [replacement-main-validation.md](replacement-main-validation.md).
The broader failures and unverified native acceptance remain blockers to declaring
the replacement fully validated or the complete issue #964 finished.
