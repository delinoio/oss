# PR #1231 scratch ownership repair

Codex thread `PRRT_kwDORRAKg86noHz1` correctly identified that recovery deleted
any staging directory found under an original operation UUID, including preview
(which never creates scratch). Original create/cleanup and restore failure cleanup
also lacked durable native-directory ownership proof.

After exclusive creation, only create/cleanup and restore now synchronize an
external version-1 claim before copying. It binds the exact request SHA-256,
operation/session/machine/preparation/snapshot/action and native root identity.
The proof remains outside payload inventories. A crash before its persistence
leaves scratch protected; current paths cannot reconstruct missing or legacy proof.
Direct cleanup and recovery require the original claim, and the anchored remover
checks opened/named root identity before chmod or unlink. Byte-identical replacement
scratch cannot borrow original ownership. Preview cannot obtain cleanup authority.

Permanent deletion joins original job owners and validates these claims before
removing scratch or the live workspace. Claims are included in copy absence
inventories and retained until coordinated metadata cleanup. Generic Worker copy
cleanup checks staging absence instead of recursively removing a reappearing
namespace. Scoped Worker/workspace instructions and the storage contract record
the same ownership requirements. No protocol number or database migration changed.

## Executed checks

- Before the fix, `TestStorageRecoveryPreservesUnownedStaging` failed for preview,
  create and cleanup: each foreign directory was treated as operation-owned.
  `TestRestoreScratchReplacementRemainsUncertain` failed because direct cleanup
  removed the replacement and settled a terminal permission failure. The complete
  negative selection exited 1 in 0.950 seconds; preserve this reproduced failure.
- The post-fix recovery/restore/scratch selection passed with race detection in
  37.009 seconds, including existing original owned-scratch recovery, lost
  completion, restore publication rejection and required-proof tests.
- The exact new ownership regressions plus missing/legacy/request-mismatched
  claims passed with race detection in 2.658 seconds. Matching replacement bytes
  remain preserved through direct failure cleanup and later explicit recovery.
- Worker `TestSessionDeletionIncludesStoredAndRestoredSnapshots` and
  `TestSessionDeletionPreservesForeignStorageStaging` passed with race detection
  in 53.538 seconds: legitimate General Chat/Worktree create/cleanup/restore
  copies and claim metadata are removed; unknown preview scratch blocks deletion
  and preserves both foreign bytes and the live workspace.

Commands used `GOMAXPROCS=2 go test -race -p 1 -count=1`, the relevant workspace
or Worker package, and 10/15-minute package bounds. Full selections are retained
in tool history; logs are `/tmp/delidev-1079-fourth-staging-{before,after,proof,
deletion}.log`. The final full-source run is separate. These focused tests do not
establish native Windows success or explain earlier native ownership/timeouts.
The final explicit creator-action guard was rechecked with the same new ownership
selection and passed in 2.165 seconds (`fourth-staging-final-proof.log`).
Historical failures, isolated reruns and source revisions remain unchanged.
