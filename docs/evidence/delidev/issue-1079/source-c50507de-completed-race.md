# Completed full race run for source c50507de

This independent record retains the complete command outcome for exact
source `c50507de8967c2fea2184a0107a4c155ed9df405`. The command ran from the existing PR worktree,
with production source and tests frozen; evidence-only changes are separate.
The pass merged main 9efb1917, then independently committed restored-directory
identity, post-rename bytes, pinned unlink and observation-lifetime repairs,
plus explicit long-path fixture preparation. It does not include later main
6c749670. See [repair details and earlier controls](pr-1231-identity-unlink-read-repairs.md)
and [long-path fixture evidence](pr-1231-long-path-fixture.md).

## Command and outcome

```sh
GOMAXPROCS=2 go test -race -p 2 -timeout=30m -count=1 ./cmds/delidev-cli/...
```

Started 2026-09-30 19:07 UTC. The complete command exited **1**.
Final completion was observed at 2026-09-30 21:29 UTC.
Log: `/tmp/delidev-1079-third-full-race.log`; SHA-256 `866ca365fdc9ce72a7ab6a2000b66e45bc43da15c3d84f95465b0f71963772d8`.

## Package results

| Package under cmds/delidev-cli | Result | Seconds |
| --- | --- | ---: |
| `internal/apiproxy` | pass | 3.714 |
| `internal/cli` | fail | 262.726 |
| `internal/connections` | pass | 27.669 |
| `internal/credentials` | pass | 6.757 |
| `internal/domain` | pass | 5.566 |
| `internal/forwarding` | pass | 2.233 |
| `internal/harness` | fail | 100.571 |
| `internal/harness/claude` | pass | 571.800 |
| `internal/harness/codex` | fail | 724.567 |
| `internal/harness/grok` | fail | 1801.132 |
| `internal/harness/nativewire` | pass | 31.487 |
| `internal/harness/opencode` | pass | 100.350 |
| `internal/integrations/github` | pass | 14.316 |
| `internal/presentation` | pass | 2.827 |
| `internal/process` | pass | 23.541 |
| `internal/providers` | pass | 10.011 |
| `internal/security` | pass | 1.660 |
| `internal/server` | fail | 1802.481 |
| `internal/store` | pass | 941.659 |
| `internal/userservice` | pass | 8.151 |
| `internal/worker` | pass | 526.629 |
| `internal/workspace` | fail | 1345.542 |

Packages with no test files: `cmds/delidev-cli`, `cmds/delidev-cli/internal/rpc`.

## Retained failed tests and diagnostics

The following inventory retains every reported failed test/subtest and its
stable caller/error lines, plus the cumulative-timeout running-test names.
Private structured native logs and full timeout stacks remain in the original
log. A passing package or isolated rerun does not erase these failures.

```text
--- FAIL: TestCLISessionAcceptanceQueueAndArchive (49.73s)
sessions_test.go:215: [session files read --id 01a0f3ba-a29b-7562-bb0c-de957d6c47da --repository-id 01a0f3ba-767a-70ea-bf2e-45830f5c47d0 --path tracked.txt]: 4 map[error:map[code:unavailable correlation_id:01a0f3ba-f041-788f-936d-d3e44bd50b8c guidance:Connect the owning Worker and refresh the workspace view. message:The workspace file reader is unavailable.] request_id:01a0f3ba-f040-7655-a580-7662c53389d8 version:1]
--- FAIL: TestDiscoveryVerifiesClaudeWithoutGrantingExecution (28.30s)
discovery_claude_test.go:92: unavailable: The operation timed out.
--- FAIL: TestDiscoveryVerifiesGrokWithoutExecution (23.97s)
discovery_grok_test.go:99: unavailable: The operation timed out.
--- FAIL: TestDiscoveryVerifiesOpenCodeWithoutExecution (20.98s)
discovery_opencode_test.go:105: unavailable: The operation timed out.
--- FAIL: TestPermissionAcceptanceMatchesExactGrantScopeAndPreservesOtherRecovery (51.35s)
--- FAIL: TestPermissionAcceptanceMatchesExactGrantScopeAndPreservesOtherRecovery/healthy (17.16s)
approval_acceptance_test.go:29: unavailable: Codex app-server did not complete its native handshake.
--- FAIL: TestPermissionAcceptanceMatchesExactGrantScopeAndPreservesOtherRecovery/prior-recovery (34.19s)
approval_acceptance_test.go:29: unavailable: Codex app-server did not complete its native handshake.
--- FAIL: TestPermissionAcceptanceRejectsForeignAmbiguousAndChangedEvidence (271.94s)
--- FAIL: TestPermissionAcceptanceRejectsForeignAmbiguousAndChangedEvidence/scope (45.80s)
approval_acceptance_test.go:72: unavailable: Codex app-server did not complete its native handshake.
--- FAIL: TestPermissionAcceptanceRejectsForeignAmbiguousAndChangedEvidence/path (17.36s)
approval_acceptance_test.go:72: unavailable: Codex app-server did not complete its native handshake.
--- FAIL: TestPermissionAcceptanceRejectsForeignAmbiguousAndChangedEvidence/missing-scope (14.72s)
approval_acceptance_test.go:72: unavailable: Codex app-server did not complete its native handshake.
--- FAIL: TestOwnedInputRetainsClaimsAndRejectsUncertainReplay (207.57s)
--- FAIL: TestOwnedInputRetainsClaimsAndRejectsUncertainReplay/input-text-method (22.01s)
input_test.go:119: recovery_required: Native request delivery is uncertain.
--- FAIL: TestInputPreflightPreservesUnusedBoundary (26.31s)
input_test.go:181: recovery_required: Native request delivery is uncertain.
--- FAIL: TestInputPublicationJoinsNativeClosure (20.80s)
input_test.go:206: recovery_required: Native request delivery is uncertain.
--- FAIL: TestInstructionChangeBeforeNativeInputRefusesClaim (11.82s)
instructions_test.go:148: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestInitialPlanRequiresOriginalClaimAckAndMode (291.28s)
--- FAIL: TestInitialPlanRequiresOriginalClaimAckAndMode/mode-result-null (19.55s)
mode_test.go:101: native initialization: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestInitialPlanRequiresOriginalClaimAckAndMode/mode-foreign (35.39s)
mode_test.go:101: native initialization: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestInitialPlanRequiresOriginalClaimAckAndMode/mode-wrong (16.82s)
mode_test.go:101: native initialization: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestInitialPlanRequiresOriginalClaimAckAndMode/mode-reused-event (24.33s)
mode_test.go:101: native initialization: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestInitialPlanRequiresOriginalClaimAckAndMode/mode-exit (31.07s)
mode_test.go:101: native initialization: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestInitialPlanRequiresOriginalClaimAckAndMode/mode-claim-failure (19.04s)
mode_test.go:101: native initialization: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestInitialPlanRequiresOriginalClaimAckAndMode/mode-bind-failure (20.59s)
mode_test.go:101: native initialization: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestInitialPlanRequiresOriginalClaimAckAndMode/mode-duplicate (27.69s)
mode_test.go:101: native initialization: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestOriginalPlanControllerPreservesUncertainty (171.52s)
--- FAIL: TestOriginalPlanControllerPreservesUncertainty/planning-unclaimed-resolution (21.90s)
plan_reply_test.go:183: recovery_required: Native request delivery is uncertain.
plan_reply_test.go:177: [private structured logs in original]
--- FAIL: TestOriginalPlanControllerPreservesUncertainty/planning-exit-after-reply (18.93s)
plan_reply_test.go:183: unavailable: The operation timed out.
plan_reply_test.go:177: [private structured logs in original]
--- FAIL: TestOriginalPlanReplyIsIndependentOfBlockedPublication (32.68s)
plan_reply_test.go:286: unavailable: The operation timed out.
--- FAIL: TestOriginalPlanReplyClaimJoinsNativeLifetimeLoss (24.63s)
plan_reply_test.go:349: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestProbeOwnsBoundedInspectedInitialization (175.00s)
--- FAIL: TestProbeOwnsBoundedInspectedInitialization/duplicate (15.11s)
probe_test.go:243: probe outcome: unavailable: Grok Build did not complete native initialization.; want unsupported
--- FAIL: TestProbeOwnsBoundedInspectedInitialization/trailing (15.21s)
probe_test.go:243: probe outcome: unavailable: Grok Build did not complete native initialization.; want unsupported
--- FAIL: TestQuestionControllerOriginalClaimsAndUncertainty (319.51s)
--- FAIL: TestQuestionControllerOriginalClaimsAndUncertainty/question-missing-automatic-resolution (22.68s)
question_reply_test.go:105: unavailable: The operation timed out.
--- FAIL: TestQuestionControllerOriginalClaimsAndUncertainty/question-publication-failure (21.47s)
question_reply_test.go:105: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestQuestionControllerOriginalClaimsAndUncertainty/question-foreign-proposal (29.34s)
question_reply_test.go:105: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestQuestionControllerOriginalClaimsAndUncertainty/question-foreign-tool (32.21s)
question_reply_test.go:105: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestQuestionControllerOriginalClaimsAndUncertainty/question-unclaimed-resolution (16.30s)
question_reply_test.go:105: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestQuestionControllerOriginalClaimsAndUncertainty/question-exit-after-reply (15.06s)
question_reply_test.go:105: unavailable: Grok Build did not complete native initialization.
--- FAIL: TestQuestionControllerOriginalClaimsAndUncertainty/question-conflicting-result (15.82s)
question_reply_test.go:105: unavailable: Grok Build did not complete native initialization.
panic: test timed out after 30m0s
running tests:
TestOriginalQuestionReplyIsIndependentOfBlockedPublication (17s)
--- FAIL: TestLocalReviewSubmissionRollsBackLinksQueueAndEvents (17.55s)
local_reviews_test.go:115: unavailable: The operation timed out.
--- FAIL: TestLocalReviewSubmissionRejectsConcurrentCommentEdit (26.93s)
local_reviews_test.go:163: unavailable: The operation timed out.
--- FAIL: TestLocalReviewSubmissionRechecksArchiveBeforeCommit (21.15s)
local_reviews_test.go:206: unavailable: The operation timed out.
--- FAIL: TestLocalReviewRejectsUnavailableObservationEvenWithStaleConsent (20.19s)
local_reviews_test.go:234: unavailable: The operation timed out.
--- FAIL: TestLocalReviewCapacityDoesNotPreventCommentRemoval (16.40s)
local_reviews_test.go:253: unavailable: The operation timed out.
--- FAIL: TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof (59.53s)
--- FAIL: TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof/matches (14.47s)
pr_remediation_workspace_test.go:21: unavailable: The operation timed out.
--- FAIL: TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof/pause (17.80s)
pr_remediation_workspace_test.go:21: unavailable: The operation timed out.
--- FAIL: TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof/unlink (16.45s)
pr_remediation_workspace_test.go:21: unavailable: The operation timed out.
panic: test timed out after 30m0s
running tests:
TestSearchRPCByteBoundsPreserveCompleteMessagesAndBothEncodings (26s)
--- FAIL: TestWorkspaceDiffUnbornAndBoundedResults (22.84s)
diff_test.go:163: unborn diff invented a commit working-tree recovery_required: The Worker workspace result does not prove the accepted preparation.
--- FAIL: TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess (62.80s)
pr_match_test.go:161: wrong native workspace match  recovery_required: The Worker workspace result does not prove the accepted preparation.
```

## Exact isolated follow-up

The unchanged isolated selection of both failed workspace cases passed; the
command and log remain in [repair evidence](pr-1231-identity-unlink-read-repairs.md).
This does not erase the full-command failures or establish their causes.

## Interpretation and limits

The full suite did not pass. Earlier controls cover only their selected
fixtures/revisions; they cannot prove the cause of every failure above.
Concurrent native suites were observed on the host, without establishing
that scheduling caused these results. No production deadline was increased
to turn uncertain native ownership into success, and no blanket baseline
exception is claimed. The log contains 0 explicit race-warning blocks.
Absence of a warning is not proof that all concurrent behavior is correct.

All earlier failed full commands, unchanged isolated reruns and controls
remain separately pinned in the issue-1079 evidence files. The completed
3530d3d9d run predates its later configuration-entry/log-fixture repairs;
89cedbb7 and 46452512 retain their distinct failures. This run cannot relabel
them. Fixtures use isolated temporary repositories and controlled local
processes, without real credentials or hosted inference. Native Windows/Linux
runtime, release acceptance, new-head CI and Codex approval remain unclaimed.
