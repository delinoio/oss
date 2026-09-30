# PR #1232: accepted address writes during graceful exit

Addresses Codex thread `PRRT_kwDORRAKg86nrB6V`. Quit closes callback acceptance under the short address-queue gate. The existing tracked worker drains previously accepted updates instead of clearing them because shutdown began. Those writes retain policy, exact child generation, reservation and profile-removal checks at staged publication, and join after native runtime return before any profile purge. UI shutdown does not wait for storage.

All 22 pinned-CEF `browser_host::tests` pass. The new regression holds storage, accepts two navigations, begins exit, rejects a post-stop callback, and uses the actual post-shutdown worker join to verify the latest accepted URL in durable and live tabs. Existing superseded-address, tab-publication and removal-denial checks also pass. These are controlled temporary-state fixtures; actual provider navigation and OS shutdown/flush acceptance remain unperformed.
