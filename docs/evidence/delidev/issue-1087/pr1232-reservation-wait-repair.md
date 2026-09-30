# PR #1232 publication-fence shutdown follow-up

Parent: `0ac6f23c46ce7725d12e65edc8c5b70cf4adb71a`, 2026-10-01.

Reviewing the newly introduced worker fence identified an unbounded UI callback
wait: native exit could stop callback delivery while the reservation worker
retained the fence, leaving the joined address worker waiting after shutdown.
Reservation waits now have a five-second bound. A short cancellation coordinator
serializes timeout with callback acceptance and rejects an unexecuted late
callback before the worker releases the fence. The UI coordinator never waits
for disk I/O. Reservation acceptance still closes the old child before Go reads.

Focused validation passed:
`cargo test -p delidev-desktop --features desktop-host,custom-protocol abandoned_ui_reservation_releases_fence_and_cannot_publish_late`.
The fixture holds the publication fence while waiting for an undelivered UI
result, expires a controlled deadline, verifies fence release, rejects late
reservation publication, and verifies a current tab control can proceed.
The fixture models callback cancellation without launching a native event loop;
actual CEF shutdown remains a separate unperformed acceptance requirement.
