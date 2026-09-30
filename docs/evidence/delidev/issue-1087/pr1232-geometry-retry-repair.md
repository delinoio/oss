# PR #1232 native geometry retry repair

Parent: `bd4da541b`, 2026-10-01.

The [review finding](https://github.com/delinoio/oss/pull/1232#discussion_r4145686295)
identified failed native resizes entering the successful-bounds cache, suppressing
later identical layout callbacks while the external child retained old bounds.
The panel now records geometry only after successful native resize for its
current open presentation, and clears the cache when resize fails.

Focused frontend validation passed:
`pnpm exec vitest run src/session-browser.test.tsx src/configuration-browser-cleanup.test.tsx --maxWorkers=1`
(10 tests in two files), and `pnpm typecheck`. The new regression fails the
first resize, triggers another callback with identical bounds, verifies the
second native request repeats those bounds, then confirms successful geometry
is cached. Mocked native commands do not prove actual platform child positioning.
The required complete frontend command is recorded separately.
