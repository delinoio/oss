# Repository registration protocol reservation prerequisite

Recorded on 2026-09-30. Inspected main revision: `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`.

Main's immutable allocation ledger reserves Worker values 3 and 4 for other original PRs, but contains no reservation for repository-inspection metadata value 5 or attachment-response support field 3. Closed, unmerged PR #1193 contains their dependent implementation. The replacement of issue #1142 therefore requires this separate main reservation before publication of the feature.

The change adds only two ledger reservations with original-PR provenance plus the protocol contract and scoped rule. Schema and generated bindings remain unchanged. It does not satisfy or close issue #1142.

Executed validation: `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs scripts/ci/ci-contract.test.mjs` passed all 28 tests; `pnpm proto:lint` and `git diff --check` passed. The descriptor allocation check accepts the new future reservations and retains every current wire assignment. No native/runtime acceptance is claimed by these checks.
