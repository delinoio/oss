# Native API relay ownership

- Follow `docs/cmds-delidev-proxy-contract.md` and the owning harness/subagent contracts. Only Go retrieves upstream credentials; execution-scoped leases retain the original account, provider, model set and revocation authority.
- OpenCode foreground tool responses must validate complete task arguments before any executable tool-call frame reaches native code. Preserve original frames and ordering in bounded private buffers; reject task reuse, background work and unknown task fields before native submission. A later observer rejection cannot substitute for this guard.
- Keep native bodies, task arguments, buffered frames and credentials out of logs, persistence and public diagnostics. Cancellation, malformed/truncated streams and rejected calls discard private buffers without a direct fallback or provider retry.
- Guard every locally generated error body with the protected values already acquired by that request, including keys returned with an error. If the fixed local body collides, preserve the failure status and safe headers with an empty body. Pre-key denials must not read credentials for error construction; failures after stream output starts retain the original abort behavior.

- Only manual Codex context actions enable completed HTTP response usage observation. Run reflection guards before handing an original hashed response/counter projection to persistence. Preserve nullable integer splits; do not collect ordinary native responses twice, infer native turn identity or log response bodies. Original request diagnostics anchor this observation even when Stop wins after submission. Follow `docs/cmds-delidev-compaction-contract.md`.

- Retain OpenCode tool-call frames through the validated terminal DONE marker, independently of finish_reason. Malformed/truncated streams, repeated calls or cancellation discard all buffered executable frames; preserve their original order only after terminal validation.

- Execution leases can return a protected credential and its original Google quota project together. Attach x-goog-user-project only after exact managed Gemini and official destination checks; downstream headers cannot supply billing authority. Preserve original execution/connection cancellation and secret clearing. Follow `docs/cmds-delidev-account-oauth-contract.md`.
