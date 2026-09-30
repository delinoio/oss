# PR #1232: attempt every shared-view recreation

Addresses Codex thread `PRRT_kwDORRAKg86no65Z`. Tab replacement had closed all shared children before its first creation error could abort the remaining attempts. The replacement coordinator now attempts every request, records each failure through the existing exact profile/generation/reservation boundary, and returns the first failure after all attempts. Existing sanitized creation logging remains in that boundary.

The controlled native regression `shared_child_recreation_attempts_every_view_after_failures` passes against the pinned CEF host. It injects different failures in the first two windows, proves the third is attempted successfully, verifies independent status failures and proves native state is unlocked during creation. This is a controlled adapter result; it does not claim native platform acceptance. The contract and native scoped instructions retain this shared-user invariant.
