# Main merge maintenance

The first PR #1221 maintenance pass found conflicts with main. Fetched main
`574c1a92c` includes Grok accounting PR #1211 and shared compaction reservations
PR #1215. Both sets of scoped instructions and documentation links were retained.
The Claude profile contract stays at its dedicated path; the shared planned
contract, original reservation provenance and unsupported profile limits remain.

Inherited Grok accounting raises the executable storage schema to 25 through its
original marked migration. Claude compaction adds no migration and cannot backfill
accounting from its counters. The completion merge retains Grok cancellation and
accounting publication alongside the Claude continuation behavior.

The new shared numbers are now established on main. This Claude feature still
uses no reserved shared enum member or SessionChange field; future shared action
resource/response extensions require compatible composition. No Codex or OpenCode
capability is advertised by the exclusive Claude context enum.

Bindings were regenerated from the merged source schemas. The focused structural
and protocol contract suite passed all six checks (1.154 s), and conflict-marker
and whitespace checks passed. Focused storage/domain migration and accounting
race checks were launched and are still running at this merge checkpoint.
