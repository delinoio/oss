# PR #1232 observed-address publication repair

Parent: `a3996bec7`, 2026-10-01.

The [review finding](https://github.com/delinoio/oss/pull/1232#discussion_r4145686265)
identified an old child's URL callback updating durable/live tabs after a new
reservation superseded it during persistence. Address persistence now stages
first and rechecks the exact profile, child generation, reservation and removal
state under the worker publication fence before replacement. Native state is
released during I/O. Address observations retain the existing control-revision
semantics and bounded coalescing worker.

Focused validation passed:
`cargo test -p delidev-desktop --features desktop-host,custom-protocol superseded_address_callback_cannot_publish_staged_url`
with the pinned CEF cache and shared target directory. The test stages an old
child's address, accepts a replacement reservation before publication, and
verifies `Stopped`, unchanged durable bytes, and unchanged live tabs consumed
by replacement preparation. The fixture does not launch a real renderer or
establish native close/flush or supported-platform acceptance.
