# Protocol reservation PR current-main repair

Recorded on 2026-09-30 for prerequisite PR #1214. The original reservation commit is `12704407b8396867092b2a8461b244c844593555`. Merge target: main `7090de046`, which includes the independently reserved native accounting implementation from PR #1211.

The conflict repair retains both appended protocol/scoped-rule sections. Repository metadata remains an allocation-only future reservation, separate from native accounting's implemented schema/capability. No existing assignments are renumbered and no metadata runtime support is activated.

Executed checks: all 28 protocol allocation, structure/relocation and CI contract tests pass; protocol lint and diff whitespace checks pass. The main-relative reservation change contains only the ledger, protocol contract, scoped rule and independent evidence. Native/runtime completion is not claimed.
