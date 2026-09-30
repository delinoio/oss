# PR #1169 review repairs and completed validation

## Revision and repair scope

Verified on 2026-09-30. Four independent Codex findings on PR head `3f4e275df72294b901274340cb192d77a257e0f5` were checked against the native subagent contract and fixed in separate commits:

| Finding | Repair and verification |
| --- | --- |
| [Child documents overflow the root stream cache](https://github.com/delinoio/oss/pull/1169#discussion_r4142084754) | `07baf167097d674a421163b0fbc648419302b75f` excludes children from the transcript/control stream cache. A real router/stream regression publishes 20 individually bounded child documents whose aggregate exceeds 8 MiB, then proves root messages and controls remain available, child documents are not fetched by the stream, and the independently paginated child view refreshes on the root revision. |
| [Output accepts a false or omitted partial marker](https://github.com/delinoio/oss/pull/1169#discussion_r4142084759) | `f6a9c1926af1d7a0ab0900b0385fe247d3895c78` requires supplied child output to declare `partial=true`. Actual SQLite publication tests reject omitted and false markers without retaining either batch member or advancing ownership/sequence; the same explicitly partial batch is accepted. |
| [Child start notifications invent ownership](https://github.com/delinoio/oss/pull/1169#discussion_r4142084767) | `b49c84cff7a5aef8dfa3afe6d09d49e261ce3a23` treats unmatched starts as discarded metadata. Tests preserve root state across early/unsolicited notifications, accept starts after canonical spawn or validated state-DB-only inventory proof, retain the original child ID and reject changed parents. |
| [Clearing a child model bypasses exact usage validation](https://github.com/delinoio/oss/pull/1169#discussion_r4142084772) | `674c5cdd88d39ad2cc72e36da41af0a99ce56851` retains each latest child usage report's original model presence/value only at receipt acknowledgment, independently of the aggregate last available model. Tests reject clearing or inventing model presence, preserve genuine omission after a prior observed model, replay a lost original receipt, and add no root billing publication. Root content/usage regression tests also pass. |

The local protocol check initially reported upstream Activity definitions missing from the older branch. Merge commit `fd94adaf77ed08f848384be8c2cda95dd3146967` integrates fetched main `36736923edfd9fcaa177a2c9594acea223e10bdf` without conflicts. It preserves both the upstream additive protocol surface and all four repairs. Required protocol checks pass on this combined source. No protocol baseline was weakened, no dependency or Rust source was authored by these repairs, and no test deadline was relaxed.

## Executed repair validation

The final checks below ran on the source committed by `fd94adaf77ed08f848384be8c2cda95dd3146967`, using isolated temporary native/provider fixtures without user credentials. Go compilation uses the separate task-owned cache `/private/tmp/issue-1094-review-go-cache`; frontend concurrency is bounded with the installed Vitest `VITEST_MAX_WORKERS=1` option.

- `GOCACHE=/private/tmp/issue-1094-review-go-cache GOMAXPROCS=4 go vet ./cmds/delidev-cli/...`: passed.
- `GOCACHE=/private/tmp/issue-1094-review-go-cache GOMAXPROCS=4 go test -race -p 2 ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker -run 'Subagent|TestClaudeUsage|TestClaudeContent' -count=1 -timeout=5m`: passed in all four packages. Each repair also passed its independent focused check before its commit.
- `GOCACHE=/private/tmp/issue-1094-review-go-cache GOMAXPROCS=4 VITEST_MAX_WORKERS=1 pnpm test` in `apps/delidev`: passed, including typecheck, 90 files / 1,110 tests, 8 bundle dry-run tests, 16 launcher/asset tests, widget fixtures and production build. The pre-merge source also passed the complete command with 86 files / 964 tests. Earlier failed frontend runs remain recorded in [implementation evidence](implementation.md).
- `pnpm proto:check`: passed after main integration, including formatting/lint, breaking checks and generated-source reproducibility.
- `git lfs fsck`, `git diff --check` and normal commit hooks: passed. Required embedded assets were generated for hooks; generated repository-owned `dist` outputs are removed before finishing.

## Completed original aggregate Go race run

The already-started execution tracked as session `66717` completed with **exit 1**. Its exact command was `GOMAXPROCS=4 go test -race -p 2 ./cmds/delidev-cli/... -timeout=30m`; it was not duplicated. The original log remains local at `/private/tmp/issue-1094-go-race.log`.

Twelve packages passed: apiproxy, connections, credentials, domain, forwarding, integrations/github, presentation, providers, security, store, userservice and worker. The command root and rpc package had no tests. Ten packages failed:

| Package under `cmds/delidev-cli/internal/` | Observed outcome |
| --- | --- |
| `cli` | Session acceptance/queue/archive fixture readiness failed. |
| `harness` | Bounded native discovery/path-failure/update-suppression fixtures failed. |
| `harness/claude` | Private API stream/native authority recovery fixture failed. |
| `harness/codex` | Approval, exact-response, effective-settings, input identity, steer and workspace authority fixtures failed. |
| `harness/grok` | Native API/content/input/plan authority and lifecycle fixtures failed; package timed out after 30 minutes. |
| `harness/nativewire` | JSON-RPC envelope, diagnostic bound and owned-process cleanup fixtures failed. |
| `harness/opencode` | Authenticated probe and owned cleanup cases failed. |
| `process` | Descendant-stop, foreign-scope/cancellation and owned cleanup fixtures failed. |
| `server` | Local-review/remediation fixtures failed; package timed out after 30 minutes. |
| `workspace` | Compilation failed because three imported package cache files were absent from the shared Go build cache. |

This long run began before the review repairs and compiled packages from a live checkout over time; it is not a revision-pinned validation of the final merged source. Concurrent host suites and shared-cache loss are observed constraints, but do not prove every failure environmental or pre-existing. The focused repaired-source results above do not turn this failed aggregate run into a pass.

## CI and acceptance limits

Before this repair push, Linux, macOS and Windows Go CI had passed on head `3f4e275df72294b901274340cb192d77a257e0f5`. Its protocol/client job passed protocol generation and both client suites but failed the existing server-preferences integration test while waiting for the Edit button. The unchanged focused test failed once locally waiting for New and passed on repeat; no product root cause was established, and its assertions/deadlines remain unchanged. The complete repaired-source frontend commands now pass. A prior single job-rerun request was rejected by GitHub; fresh CI after the single repair push must be assessed independently.

Fresh remote checks and reviews are pending after a push. No merge readiness, human approval, real provider-account session, native platform installation or release acceptance is claimed. The broader issue #964 scope remains independently outstanding.
