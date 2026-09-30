# Review clarification of the naming boundary

## Source and revision

- Date: 2026-09-30.
- Issue: https://github.com/delinoio/oss/issues/1136.
- PR: https://github.com/delinoio/oss/pull/1172.
- Reviewed implementation: `f65e1a8d4efb4b22673fff3c3e3f2420bd5ab0b1`.
- Inspected current head before this clarification: `93b468eaefa12cb2afa35fbfb2d7818710d87772`.
- Naming review: https://github.com/delinoio/oss/pull/1172#discussion_r4141842106.
- Evidence review: https://github.com/delinoio/oss/pull/1172#discussion_r4141842114.

## Scope clarification

Issue #1136 targets existing **Execution Worker(s)** presentation copy and the New session **Runs on** selector. Its Out of Scope section explicitly excludes replacing every generic **Worker** or **execution machine** reference. The naming review correctly exposed broader wording in the newly added contracts, but its proposed rename of existing generic execution-machine copy exceeds that issue scope.

The desktop and CLI scoped naming rules, desktop contract and project invariant now name the original **Execution Worker(s)** copy explicitly and preserve generic **execution machine** references. The paired-device details, local Worker controls, session workspace surfaces and generic server errors remain within that preserved boundary. No source, execution behavior, protocol or stored identifier changes in this repair.

The evidence review inspected the implementation commit before the separate evidence commit. The independent [implementation validation record](runner-device-terminology.md) is already present in `93b468eaefa12cb2afa35fbfb2d7818710d87772`, including actual commands, passing focused checks and frontend results, the incomplete/not-passing broad Go run and its unresolved qualifications. The frozen historical ledger remains unchanged.

## Verification and limits

- `node --test scripts/ci/delidev-structure.test.mjs`: all three documentation ownership checks passed.
- `git diff --check`: passed.
- Code inspection confirmed that the generic execution-machine strings cited in the review were outside the issue's requested **Execution Worker(s)** replacements.
- This repair changes Markdown only. It does not rerun frontend or native behavior validation, establish a complete local broad Go result, or replace the implementation record's evidence limits. CI and review evidence must be assessed for the new head separately after publication.
