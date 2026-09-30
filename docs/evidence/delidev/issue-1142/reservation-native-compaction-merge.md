# Repository metadata reservation after native-compaction reservation

Recorded on 2026-09-30 for prerequisite PR #1214. Merge target: main `574c1a92c`, which established the independent native-compaction reservations through PR #1215. Previous metadata reservation head: `521752664`.

Main now owns Worker value 5 for native compaction. This repair preserves that meaning and assigns repository-inspection metadata value 6; attachment-response support remains field 3. The original PR #1193 provenance and historical Worker value 5 remain recorded without redefining any main number. The protocol contract and scoped rule describe the new reservation.

The allocation-test conflict is composed: keep main's original-PR/owning-issue and shared-consumer validation alongside the new baseline declaration-kind validation. Both kinds of invariant remain active across every reservation.

Validation: all 28 protocol allocation, structure/relocation and CI contract tests, protocol lint and diff whitespace checks pass. No schema, generated binding or runtime capability is activated. The prepared feature still requires this reservation on main, then a reconciled value-6 schema generation and renewed verification before publication.
