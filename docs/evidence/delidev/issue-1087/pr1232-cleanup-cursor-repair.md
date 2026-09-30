# PR #1232 fair offline browser cleanup

Parent: `0295230e6`, branch `kdy1/delidev-browser-1087-replacement`, 2026-09-30.

The [review finding](https://github.com/delinoio/oss/pull/1232#discussion_r4144983918)
identified starvation when the first 64 offline acknowledgment intents remained
in a stable directory iteration order. A private atomic cursor now rotates the
sorted account-removal inventory across exits. Progress persists before local
purge and server acknowledgment; original ownership, retained receipts, native
shutdown prerequisites, and the existing 64-intent/45-second loop bounds remain.

Focused validation passed:
`cargo test -p delidev-desktop --features desktop-host,custom-protocol offline_receipts_rotate_across_exits_without_starving_later_profiles`
with the existing pinned CEF cache and shared target directory. The fixture
creates 65 pending profiles with an offline sidecar, verifies that the first exit
purges exactly 64, constructs a new host over the retained state, and verifies
that the next exit removes the remaining profile while all 65 acknowledgment
receipts remain pending. This exercises durable progress across host instances;
it does not establish real renderer shutdown or provider/platform acceptance.
Full repair validation is recorded separately after the final independent fix.
