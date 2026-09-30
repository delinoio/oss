# OpenRouter OAuth foundation and integration blockers

Issue: [#1146](https://github.com/delinoio/oss/issues/1146).
Inspected main revision: `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`, freshly fetched on 2026-09-30.
This is preparatory implementation evidence. The OAuth feature is incomplete and
must not be advertised, enabled, or used to close the issue.

## Prepared portable primitives

- Exact 1–8192-byte UTF-8 authorization-code validation rejects control characters
  and preserves spaces and original bytes, including rejecting terminal newlines.
- Canonical localhost callback validation rejects nonlocal hosts, alternate IP
  spellings, user information, noncanonical ports, escaped paths, query strings,
  fragments, and short paths. This does not prove a native listener owns a URL.
- OAuth eligibility requires the saved managed OpenRouter preset and the exact
  enabled official endpoint/protocol/bearer contract. Editable names grant nothing.
- Server-side authorization construction uses 32 bytes of cryptographic entropy,
  a base64url verifier and S256 challenge, with an optional callback and headless
  authorization when omitted. Test entropy is injected; production defaults to
  `crypto/rand.Reader`.
- A bounded exchange helper addresses only the fixed OpenRouter HTTPS POST. It
  rejects invalid inputs before networking, disables proxies, redirects,
  compression and keepalive reuse, caps headers at 32 KiB and responses at 64 KiB,
  and uses a 20-second deadline. Ambiguous, malformed, duplicate, oversized, or
  unsuccessful responses return sanitized recovery guidance without retry.
- Returned API keys pass the existing API-key validator. Request/response buffers
  are cleared; no raw upstream error or identity enters a returned problem.

The [official OpenRouter OAuth documentation](https://openrouter.ai/docs/guides/overview/auth/oauth)
was read on 2026-09-30 to verify S256, arbitrary-port localhost callbacks,
headless authorization and the fixed code-exchange endpoint. No real OpenRouter
account, browser session, credentials or provider HTTP exchange was used.

The helper has no caller in the product. Its eventual coordinator must persist
an irreversible exchange-dispatch claim before calling it; a one-call HTTP helper
alone cannot establish once-only behavior across RPC retries or process restarts.
No RPC, CLI OAuth command, provider capability, SQLite attempt table, native
listener/opener or waiting UI has been integrated in this preparatory change.

## Integration decisions required

1. Issue #1146 requires [#1145](https://github.com/delinoio/oss/issues/1145)'s direct
   provider picker first. #1145 remains open, with no matching PR found. Keep that
   separate dependency or explicitly include its approved prerequisite scope.
2. `cmds/delidev-cli/internal/store/migration-reservations.json` intentionally
   retains versions 25/26/27 for replacement work from #1108/#1115/#1117. All three
   PRs are closed without merging. The store instructions require reservation
   changes to be established on main before dependent branches; the replacement
   workflow requires the reserved order and forbids no-op gap migrations.
   OAuth therefore cannot simply claim schema 25 or execute migration 28 against
   the current schema-24 base. A reviewed change to that ordering, or the preceding
   implementations, is needed before integrating durable attempt storage.

The existing schema version, reservation ledger, public contracts, generated
bindings, and product capabilities remain unchanged. Prepared helpers are kept
on the isolated issue branch; no completion PR or maintenance heartbeat exists.

## Validation

- `go test ./internal/domain ./internal/providers`: passed.
- `go test -race ./internal/domain ./internal/providers`: passed after the final
  response-buffer clearing change.
- `go vet ./internal/domain ./internal/providers`: passed.
- `go vet ./...` from `cmds/delidev-cli`: passed.
- `go test -timeout=20m ./...` from `cmds/delidev-cli`: completed with exit status 1, including CLI and Claude harness failures and
  20-minute Codex harness/workspace package timeouts. This is not a passing
  full-suite result.
- `go test -json -timeout=20m ./internal/cli`: failed at
  `TestCLISessionAcceptanceQueueAndArchive`, `sessions_test.go:202`, where
  `session create --wait` returned a typed operation timeout after durable
  acceptance. The original full run also reported Claude probe cleanup,
  late-acknowledgment and owned-scope timing failures. Their baseline cause has
  not been established; no unrelated execution-lifecycle repair is claimed.

The new OAuth entrypoints are referenced only within their helpers and tests;
no existing product path invokes them, as verified with `rg`. Existing CLI and
harness execution implementations were not modified. These observations do not establish that the broader failures are
pre-existing or that full validation passes.
Fixtures do not prove native callback behavior, actual provider acceptance,
server recovery/cancellation, or Windows/Linux/macOS OAuth integration.
