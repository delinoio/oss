# PR #1182 frontend CI repair

Date: 2026-09-30. Repair starts from merge commit `2967b1e6ffe18893ba7c986f65de7cbb7b19899b`, including main `9d110ced702e66bb50974c5ec98e830b86adbe5b`. Host: macOS arm64.

GitHub Actions run `36682853287`, job `109782014301`, reported 968 passing tests and one failure in `settings-preferences.integration.test.tsx`: the first post-save Edit Server preferences lookup exhausted Testing Library's default one-second wait. CI Result failed because that job failed; these are one repair problem. The Activity fixtures passed in that run. The job log does not establish the exact production RPC latency.

Wrap only the owned fixture's first real `SaveConfiguration` acknowledgment with a 1,200ms delay after the server response. With the original lookup timeout, `pnpm exec vitest run src/settings-preferences.integration.test.tsx --maxWorkers=1` reproduces the same missing Edit Server preferences failure. Keep the delay as regression coverage and bound the asynchronous catalog/post-save lookups to five seconds within the existing 15-second test limit. Preserve every default-document, singleton, ID and revision assertion, and assert that the delayed real acknowledgment occurred. No production RPC, retry policy or timeout changes.

Generated DeliDev client build and desktop typecheck passed. After the wait repair, `pnpm exec vitest run src/settings-preferences.integration.test.tsx src/activity-sidebar.test.tsx src/activity.test.tsx src/pull-requests.test.tsx --maxWorkers=1` passed all 25 tests across four files. This also checks the second main merge's Activity PR source and neighboring Pull requests presentation. The exact source-icon LFS asset was hydrated and verified from the local cache before remaining frontend packaging/launch checks.

These results do not prove native CEF geometry, focus or platform acceptance. The original limits in `activity-sidebar-validation.md` remain.
