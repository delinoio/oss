# PR #1232 asynchronous child-creation failure repair

Parent: `a1e86983a`, 2026-10-01.

The [review finding](https://github.com/delinoio/oss/pull/1232#discussion_r4145686316)
identified request-context callbacks logging failed child creation after open
had already returned success, leaving polling to report an apparently healthy
blank view. Child creation now records its typed failure only for the matching
profile, generation and current reservation. Native state polling and ordinary
controls return that failure; Hide still releases the view, and accepted
removal remains separately observable. Explicit Retry reserves a new view with
no inherited failure. Logs retain only the operation and stable error code.

Focused validation passed:
`cargo test -p delidev-desktop --features desktop-host,custom-protocol asynchronous_creation_failure_is_visible_only_to_its_exact_view`
with the pinned CEF cache and shared target directory. It verifies failure
visibility, ordinary-control rejection, late-failure exclusion from replacement
views, and separate removal progress. Frontend
`pnpm exec vitest run src/session-browser.test.tsx src/configuration-browser-cleanup.test.tsx --maxWorkers=1`
passed all 11 tests, including delayed polling failure followed by explicit
retry with a new presentation UUID; `pnpm typecheck` also passed. These native
state and mocked invocation tests do not establish real CEF startup/failure or
supported-platform acceptance. Full validation is recorded separately.
