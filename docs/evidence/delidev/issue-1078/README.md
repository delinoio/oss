# Issue #1078: permanent session deletion

## Implementation

Replacement based on main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` on
2026-09-30. The preserved implementation from closed PR #1116 was adapted to the
service-specific schemas and scoped source ownership, with capability value 9
from the main allocation ledger. Generated bindings were regenerated rather
than transplanted. SQLite remains schema 24; no reserved migration is activated.

Current forwarding is composed into deletion: Stop is requested atomically,
both original peer cleanup receipts gate purge, and cleanup reports remain valid
after session admission closes. Both primary and auxiliary Worker lanes retain
the outer job lock through final journal and report publication.

## Validation

Fresh validation is recorded below when commands finish. Historical PR results
are not treated as validation of this replacement.

## Limits

Tests use isolated temporary SQLite, Git, process and socket fixtures without
credentials or hosted inference. Native Windows/Linux, real-account and package
distribution acceptance have not been performed. Workspace snapshots, dependent
Sidechats and terminal product families remain unimplemented on this baseline;
their future resource owners must join the deletion graph before activation.
Reclaimed filesystem allocation remains unknown even after confirmed cleanup.
