# Issue #1145 clicked-provider review repair

Validated the repair working tree based on `7cbb2e2a6ac0d927179976429c542f067a88034a`
on 2026-09-30 for [the Codex finding](https://github.com/delinoio/oss/pull/1183#discussion_r4142192395).

A retained account-filter page could override the exact clicked provider summary
with an older disabled or different authentication contract. The form now uses
that one clicked summary before either independent inventory page. Later page
refreshes cannot replace it. Existing fresh `GetResource` verification still
validates or rejects its identity, authentication, protocol, endpoint and enabled
state before creation and connection.

After rebuilding the API client, `pnpm typecheck` passed. The focused
`pnpm exec vitest run src/account-settings.test.tsx --maxWorkers 1
--no-file-parallelism --testTimeout 30000` run passed all **28 tests**. Two new
keyed/keyless regressions retain the clicked contract against both an initially
stale account-filter page and a later stale eligible page, then prove one explicit
save/connection uses the selected method. Existing authentication/disablement
mismatch regressions still reject writes and clear the submitted key.

No repository timeout/concurrency setting changed. These fixture results do not
establish native CEF or real hosted-provider acceptance.
