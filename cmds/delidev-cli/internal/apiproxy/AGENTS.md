# Native API relay ownership

- Follow `docs/cmds-delidev-proxy-contract.md` and the owning harness/subagent contracts. Only Go retrieves upstream credentials; execution-scoped leases retain the original account, provider, model set and revocation authority.
- OpenCode foreground tool responses must validate complete task arguments before any executable tool-call frame reaches native code. Preserve original frames and ordering in bounded private buffers; reject task reuse, background work and unknown task fields before native submission. A later observer rejection cannot substitute for this guard.
- Keep native bodies, task arguments, buffered frames and credentials out of logs, persistence and public diagnostics. Cancellation, malformed/truncated streams and rejected calls discard private buffers without a direct fallback or provider retry.

- Responses creation SSE `type: error` uses top-level native codes and a flat Responses event. Keep HTTP, Chat, Anthropic and nested `response.failed` envelopes separate. Redact the message, clear `param`, bound sequence numbers to nonnegative JSON-safe integers, and retain closed classification, protected-code fallback and once-only diagnostic/lease settlement under the proxy contract.

- Only manual Codex context actions enable completed HTTP response usage observation. Run reflection guards before handing an original hashed response/counter projection to persistence. Preserve nullable integer splits; do not collect ordinary native responses twice, infer native turn identity or log response bodies. Original request diagnostics anchor this observation even when Stop wins after submission. Follow `docs/cmds-delidev-compaction-contract.md`.

- Retain OpenCode tool-call frames through the validated terminal DONE marker, independently of finish_reason. Malformed/truncated streams, repeated calls or cancellation discard all buffered executable frames; preserve their original order only after terminal validation.
