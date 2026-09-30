# PR #1232 tab-control publication repair

Parent: `03ba34c18c3b7b42c057619e8da429e73ae1423a`, 2026-10-01.

The [review finding](https://github.com/delinoio/oss/pull/1232#discussion_r4145686254)
identified obsolete NewTab/SelectTab/CloseTab writes reaching disk before the
final reservation check. Private JSON writes now stage and fsync separately,
then recheck exact ownership under a worker publication fence before replacement.
Reservation acceptance remains on the native UI loop, before Go authorization;
its coordinating worker holds the same fence until the callback returns. Native
state is released during disk I/O, and the UI callback never waits for the fence
or storage. Dropped obsolete staging files leave durable and live tabs unchanged.

Focused validation passed:
`cargo test -p delidev-desktop --features desktop-host,custom-protocol superseded_tab_controls_discard_staged_files_before_publication`
using the pinned CEF cache and shared target directory. The fixture pauses
publication after staging each of the three controls, accepts a new reservation,
and verifies `Stopped`, unchanged durable/live tabs and removal of the staged
file. It also verifies native state remains available while publication waits.
The fixture does not launch a real CEF child or establish platform acceptance.
Git LFS integrity, required asset preparation, API-client build and frontend
build passed before native compilation. Full validation is recorded separately.
