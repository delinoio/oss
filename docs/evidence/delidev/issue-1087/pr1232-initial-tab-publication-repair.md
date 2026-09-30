# PR #1232 first-tab publication follow-up

Parent: `fb90e49`, 2026-10-01.

Inspection after the two publication repairs found first-tab initialization
still using the old publish-before-reservation-check sequence. An initial open
superseded during disk writes could leave its starting address for replacement
preparation despite returning `Stopped`. It now stages the first tab and uses
the same exact-reservation worker fence before durable/live publication. The
private staging helper also closes its file before error cleanup, as Windows
cannot remove an open staging file.

Focused validation passed:
`cargo test -p delidev-desktop --features desktop-host,custom-protocol superseded_initial_open_discards_its_staged_first_tab`.
The fixture stages the old initial open, replaces its reservation, verifies no
durable tabs or runtime profile were published, then verifies the replacement's
own initial address. Native state remains available while publication waits.
The test uses temporary local state and does not launch a real CEF child.
The compile reported one unnecessary mutable binding, removed before commit;
final native tests and strict Clippy validate the corrected source separately.
