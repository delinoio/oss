# Manual push handling: explicit fixture write permission

Date: 2026-09-30. Base: `57a37cd92`. Revision: the enclosing commit.
This supplements the [dispatch permission record](dispatch-permission-2026-09-30.md)
and the [third main reconciliation](third-maintenance-main-2026-09-30.md).

The expanded reconciliation checks found that three positive synthetic store
handling scenarios still froze an ordinary Agent's default permission, then
attached manual remediation to its completed assignment. The new immutable
assignment validation correctly rejected that combination, retaining uncertain
ownership and publishing no handling proof. This was a stale fixture expectation,
not grounds to weaken the permission guard.

Positive manual scenarios now select workspace-write on the fixture Agent before
the original session claim freezes configuration and its digest. New default and
read-only scenarios retain those exact original permissions and prove that native
success/push-shaped output still cannot handle evidence, publish verification or
release uncertain remediation ownership. Existing foreign selection, missing/
uncertain proof, native failure, dismissal and both Worker clock-skew cases remain.

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/store -run 'PRFix|PRRemediation|PRActivity|CIProblem|Queue' -count=1 -timeout=10m`
passed in 25.742 seconds. This independently replaces the failing store selection
reported in the reconciliation record; the earlier combined command itself remains
a failed observation. `git diff --check` passed. This changes only test/evidence
source, without production, frontend, Rust, schema or migration edits.

The passing store group and the other separately passing reconciliation packages
are focused coverage, not a complete Go-suite pass. The complete frontend command
passed on the reconciled source as recorded separately. Synthetic store output is
not live native Git/account evidence, and the original executable/configuration
verification-to-execution P1 remains unresolved. New-head CI/review approval awaits
the next heartbeat after the final push.
