# Historical subscription evidence from closed PR #1124

These are retained observations at commit `9442a2586a1ab2135d66bf3dd94b808b4eedcf6a` from the prior branch, not checks executed on this replacement.

## Managed Codex subscription ownership — issue #1095 (2026-09-30)

The [subscription contract](../../../cmds-delidev-subscription-contract.md) adds authenticated
Connect/CLI login, explicit browser/device presentation, cancellation, refresh and
logout, plus an independently protected paired-Worker channel. Codex `0.151.0`
uses private native file authentication; API/discovery keep their ephemeral
profile. One durable account lease fences lifecycle work and execution by the
original device, machine, instance, operation, server epoch and generation.
Waiting executions retain their selected account through refresh. Missing
write-back or cleanup retains recovery-required ownership and denies old bundles.

Controlled native subprocess fixtures passed under the race detector for
browser/device completion, cancellation, changed/unchanged refresh evidence,
local logout and symlink refusal. Worker composition fixtures passed for those
operations and lost upload; they verify durable original completion identities,
process/home cleanup before publication and secret-free remaining files.
Real temporary Connect/SQLite fixtures passed for concurrent lease claims,
cancellation/revocation before publication, independent accounts, duplicate
provider identity refusal, rotated-generation waiting and API relay denial.
Ordinary responses and SQLite/WAL/SHM files are scanned for synthetic credentials.
CLI checks cover explicit Worker selection and original operation/receipt IDs.

Executed validation: focused subscription/adapter/Worker/server/CLI race tests,
DeliDev-wide Go vet, Buf formatting/lint and breaking compatibility, API-client
tests (41 tests) and typecheck, and root Go formatting with explicitly generated
embedded assets. The first complete race run exceeded the default ten-minute
package deadline under concurrent machine load and also reported existing
workspace-reader timeouts. The reduced-concurrency run reproduced the existing
CLI workspace-diff deadline failure; its remaining packages are still running.
Focused subscription checks and complete compilation pass; the full race suite
is not reported as passed. An isolated source archive of the unchanged target
revision `b741cec88d68ba84eaf918bbee22ca28bff57ec6` also failed
`TestCLISessionAcceptanceQueueAndArchive` at its creation-diff read with the same
workspace-reader-unavailable classification (39.78 seconds); the focused current
branch run failed at its file read (60.58 seconds). This confirms the existing
reader failure on the baseline without treating either run as passing.

No real subscription login, hosted inference, installed-Codex subscription
acceptance, native Windows/Linux subscription runtime, release, desktop login
controls or full uncertain-lease recovery is claimed. All authentication material
is synthetic, all runtime state is temporary, and no user credential is imported.
The complete issue #964 requirements remain open beyond this increment.


Maintenance of [PR #1124](https://github.com/delinoio/oss/pull/1124) found a
regression-test protobuf request copied by value, including its internal mutex.
The test now constructs a fresh request with the original fenced fields instead.
All focused subscription server race tests pass (21.387 seconds), current
DeliDev-wide and repository-wide (`go vet ./...`) vet pass, and root
formatting reports no changes. The full race
suite limitation above remains visible; this test-only repair changes no product
contract or authentication authority.
