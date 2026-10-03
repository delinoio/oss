# Native API relay ownership

- Follow `docs/cmds-delidev-proxy-contract.md` and the owning harness/subagent contracts. Only Go retrieves upstream credentials; execution-scoped leases retain the original account, provider, model set and revocation authority.
- OpenCode foreground tool responses must validate complete task arguments before any executable tool-call frame reaches native code. Preserve original frames and ordering in bounded private buffers; reject task reuse, background work and unknown task fields before native submission. A later observer rejection cannot substitute for this guard.
- Keep native bodies, task arguments, buffered frames and credentials out of logs, persistence and public diagnostics. Cancellation, malformed/truncated streams and rejected calls discard private buffers without a direct fallback or provider retry.
