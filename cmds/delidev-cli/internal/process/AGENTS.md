# DeliDev process ownership

- Follow the parent instructions and `docs/cmds-delidev-process-contract.md`. Native scope journals and joined cleanup retain their original ownership and uncertainty gates.
- `ControllerIdentity` observes an infrastructure controller only. Validate its exact PID/kernel-birth shape and use the closed Alive/Exited/Unknown outcomes. Permission, malformed, unavailable and unsupported reads cannot prove exit; boolean liveness and process-list scans are not proof.
- Independently verified PID reuse or definitive kernel absence proves only the original controller exited. Never adopt or terminate the unrelated current process, reconcile native descendants, remove journals or infer execution cleanup from this observation.
- Preserve Linux boot/start ticks, Darwin kernel timestamps and Windows creation FILETIME. Windows exit state and its final birth comparison use the same retained query handle. Linux signal-zero absence checks deliver no signal; missing procfs data alone grants no proof.
- Keep safe phase/code logs separate from PID, birth, paths and native contents. Record platform acceptance separately from temporary-process fixtures, race tests, cross-compilation and packaging.
