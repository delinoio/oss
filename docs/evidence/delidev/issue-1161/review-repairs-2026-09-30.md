# Home navigation review repairs: 2026-09-30

PR: https://github.com/delinoio/oss/pull/1200. Repairs follow the published `e519249c0f012a8609c9497eb51cded2c7aaf68c` review and the main/header merge recorded separately in `maintenance-2026-09-30.md`.

## Accepted refresh boundaries

Review thread: https://github.com/delinoio/oss/pull/1200#discussion_r4142465410 (`PRRT_kwDORRAKg86ncpSk`). A refresh previously followed newly returned continuations and could replace/truncate accepted ranges or expose a newly discovered trailing continuation. It now reads the exact accepted request tokens, retains those opaque tokens and validates continuation presence including final exhaustion plus the end-row identity of nonterminal ranges, retains the entire accepted snapshot on boundary drift and returns the typed cursor-expiry recovery surface. Only explicit Reload list accepts new boundaries. Exact failed-range Retry still reads that range alone and preserves neighboring ranges.

`pnpm exec vitest run src/home-navigation.test.ts --maxWorkers=1 --fileParallelism=false --testTimeout=30000` from `apps/delidev` passed 1 file / 9 tests, including changed first boundaries, premature exhaustion, newly extended exhausted tails and isolated failed-refresh retry. The API client was generated explicitly before testing. Component evidence does not establish native acceptance; review-thread resolution waits for the single repair push.


## Global failure disclosure

Review thread: https://github.com/delinoio/oss/pull/1200#discussion_r4142465418 (`PRRT_kwDORRAKg86ncpSq`). Global sessions are an unfiltered source for off-catalog fallback groups as well as General Chat. Their read failure/previous-data notice and Retry/Reload list now remain outside the General Chat disclosure. Collapsing General Chat cannot hide a failure affecting visible fallback rows or their recovery action.

`pnpm exec vitest run src/sidebar.test.tsx --maxWorkers=1 --fileParallelism=false --testTimeout=30000` passed 1 file / 23 tests. The added unavailable and typed cursor-expiry cases retain the same visible fallback row, selection and focus, recover only the failed global scope, leave catalog reads unchanged and keep General Chat collapsed. Thread resolution waits for the single repair push.

Source inspection of `internal/security/identity.go` and the Resource/Session list handlers confirms that outgoing cursors renew their 24-hour expiry on each read. A renewed token is not itself boundary drift. The follow-up validation keeps original request/continuation tokens, accepts renewed opaque tokens when the nonterminal end-row identity and continuation presence agree, and never decodes cursor content.

After the opaque-renewal refinement, `pnpm exec vitest run src/home-navigation.test.ts src/sidebar.test.tsx --maxWorkers=1 --fileParallelism=false --testTimeout=30000` passed 2 files / 33 tests. The new positive renewal case preserves original request/continuation tokens across two refreshes while accepting exact higher-revision rows; shifted/truncated/extended boundaries still stop with explicit scope-local recovery.
