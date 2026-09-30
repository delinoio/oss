# Pinned required workflows: complete local validation outcomes

Validation date: 2026-09-30. Implementation is
`910d33b73d6d02552fe63061a94768ff4fabd090`, based on fetched main
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250`. The additional commits record evidence
only. See [focused validation](replacement-focused-validation.md) for the successful
domain/adapter race tests, Go vet, three related desktop files / 16 tests, typed
client build/type checking, 113 CI contracts and read-only schema observations.

## Full commands

- Root `go test -race ./cmds/delidev-cli/...` completed with exit 1. Domain and
  GitHub adapter tests passed. Failures occurred in CLI, harness, its Claude/Codex/
  Grok/nativewire/OpenCode adapters, process, server, store, Worker and workspace.
  Several packages reached the default ten-minute watchdog; other failures include
  fixture readiness and independently confirmed cleanup. None of the failing
  runtime/test files were modified in this change. This run does not establish a
  full Go-suite pass or independently attribute every failure to main or the host.
- Desktop `VITEST_MAX_WORKERS=2 pnpm test` completed with exit 1: 92 files passed,
  4 failed; 1,190 tests passed, 55 failed, 1,245 total, 687.48s. Failures are in
  unchanged `App.test.tsx`, `settings.test.tsx`, `settings-models.test.tsx` and
  `agent-configuration.test.tsx`. Thirty-nine report the 5s/15s test watchdog;
  others include incomplete asynchronous UI observations. The three issue-specific
  CI/rule/workflow files passed. Two failed Agent opaque-page cases passed in the
  isolated retry recorded above. This neither attributes all failures nor turns
  the full command into a pass. Concurrent sibling validation was observed on the
  same host; it is contextual evidence, not conclusive attribution.

## Additional completed checks

- `GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/server -run TestRepositoryQueriesExposeOnlyMatchingPRObservationFamily -count=1`
  passed (5.413s), exercising authenticated CI publication through the existing
  exclusive query family. The combined server/CLI invocation's CLI regex matched
  no cases; it is not counted as CLI verification.
- `GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/cli -run '^TestCLIGitHubPRObservationCommandsUseExactNumberAndPage$' -count=1`
  passed (3.158s), covering equivalent CLI query framing and exact PR identity.
- After the failed unit stage stopped the aggregate frontend script, the remaining
  steps were executed explicitly: `pnpm test:bundle-dry-run` passed 8 tests,
  `pnpm test:desktop-launch` passed 16 tests, `pnpm test:widget` passed the macOS
  widget fixtures, and `pnpm build` produced the production bundle successfully.
- A read-only merge-tree check against freshly fetched main
  `7090de046` completed without textual conflicts. It neither changed the issue
  branch nor substitutes for CI against GitHub's actual test merge.
- Required icon LFS hydration and `git lfs fsck` passed. Repository-generated
  frontend/client `dist` directories were removed after validation; no generated
  binaries are committed.

No real-account pinned-required-workflow, native desktop or release acceptance is
claimed. The full-suite failures remain visible for review; issue #964's other
requirements, unsupported workflow profiles and native evidence gaps are retained.
