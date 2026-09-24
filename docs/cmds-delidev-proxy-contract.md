# DeliDev native API relay contract

## Scope
`cmds/delidev-cli/internal/apiproxy` implements the internal server-only native HTTP/JSON/SSE relay. The server router now registers the relay with durable execution authority and native-reference storage. Public session dispatch and Worker token delivery remain pending, so ordinary session commands cannot yet produce an authorized execution assignment. This document describes the implemented relay and authority boundaries; fixture authorization does not establish complete native execution acceptance.

## Runtime and Language
Go, using the root module, `net/http` and the existing typed domain model. Native provider SSE is an internal harness protocol, not a DeliDev client event API; product communication continues to use authenticated Connect.

## Users and Operators
The server resolves immutable execution authority and reads the exact protected account connection. A native harness on the selected Worker receives only its execution credential and relay endpoint. Users do not receive general-purpose external proxy keys or caller-selectable upstream destinations.

## Interfaces and Contracts
An `Authority` acquires a revocable `Lease` for a canonical `ddv_exec_` token containing 32 bytes of URL-safe random credential material. A scope binds canonical UUID-v7 execution, session, account, connection, provider and model identities; one validated provider endpoint/protocol/authentication policy; one exact native model; and a nonempty protocol-compatible operation allowlist. Owner/device bearer tokens are not execution credentials. The server authority authenticates the digest and rechecks current execution/connection/Worker authority.

The `RegisterExecution` Worker RPC accepts only the SHA-256 digest of a Worker-generated token and binds it to the exact claimed job revision, execution, machine, instance, device and current server process epoch. One job can register only one immutable binding; exact receipt retries preserve it and recheck live authority. Every acquisition rechecks the claimed uncanceled job, matching first-execution configuration/input identities, enabled machine, nonrevoked Worker, fresh 45-second Worker lease, active unrecovered session, current project restrictions and exact enabled/ready API account connection. The initial profile grants only Codex `0.151.0` OpenAI Responses creation/compaction. A changed account connection, stale/replaced Worker or new server process cannot reuse a stored grant. Native dispatch integration must extend this first-execution scope explicitly before later-turn execution can use it.

Only exact POST paths under `/api-proxy/v1` are recognized: `/chat/completions`, `/responses`, `/responses/compact`, `/messages`, and `/messages/count_tokens`. Each lease must specifically authorize its operation. Query strings, escaped paths, arbitrary methods and absolute-form proxy destinations are rejected. JSON must contain the scope's exact `model`; duplicate fields, ambiguous control-field casing, alternate `models`, and fallback `route` selectors are rejected before secret access. Compaction and token-count requests cannot request streaming. Native request bodies otherwise retain their bytes and provider-specific fields; the relay does not translate protocol families or invent native capabilities.

Responses `previous_response_id` and `conversation` references require an execution-authority callback to verify ownership before key access. Absence of that callback fails explicitly. Returned native response identities are observed only after response/secret checks and before delivery. The durable reference index binds session, account, connection, canonical model, reference kind and exact native identifier; a different scope cannot borrow continuity. References are capped at 10,000 per session, and session deletion removes them. Cross-execution account switching and native file/item/conversation operation families still require explicit adapter integration; this initial allowlist does not authorize those additional routes.

Successful bounded JSON and LF/CRLF SSE preserve native bytes except that native diagnostic error objects are replaced with safe protocol-shaped errors. Native machine codes from a closed allowlist retain distinctions needed for context limits, authorization, rate limits and overload. Native streaming ends only on its protocol terminal event; truncated/invalid streams abort the downstream response without forged completion or proxy retries. Native harness retry decisions remain the harness's responsibility. HTTP 4xx/5xx status and a bounded `Retry-After` survive; provider cookies, redirect locations, diagnostic messages, challenges and request identifiers do not.

There are at most 16 concurrent authorized requests, with no wait queue; request/JSON/frame limits are 32 MiB, total SSE is 256 MiB, and the request deadline is 15 minutes. Request-body reads and each downstream write have 30-second deadlines. Dial/TLS handshakes are bounded to 10 seconds; inference response headers have a 10-minute deadline and a 32 KiB limit. Native error JSON inspection is separately bounded to 64 KiB and two seconds. Bounds are operational limits, not permission to replay a possibly accepted request.

## Storage
The relay itself persists no tokens, upstream keys, prompts, responses or usage. Its server authority uses schema v7's private grant and native-reference indexes, with synchronized pre-migration backups and transactional upgrade from v1-v6. Job deletion removes its grant; session deletion removes its references. Registration receipts contain only the job identity, and reference-observation receipts contain no native content. Token delivery must not put raw credentials in SQLite, job documents, events, argv, transcripts or ordinary output. Harness JSON remains the sole usage source; relay traffic cannot estimate tokens or costs.

## Security
Plain HTTP is accepted only from an actual loopback peer; remote callers require TLS. Browser-origin/cookie/fetch metadata paths are denied. Each request accepts exactly one bearer or `x-api-key` execution credential. The native account key is retrieved only after body/scope authorization and a second account-gated authority check, injected into the single saved upstream endpoint, and cleared from the mutable byte buffer after use. Keyless accounts never read a key. Each lease observes durable changes and checks lease freshness at least once per second; cancellation covers finished/canceled/uncertain jobs, account disconnection/replacement, Worker/device revocation, changed restrictions and server shutdown. Account disconnection and server shutdown cancel and join active relay handlers before deleting credentials or closing their storage. This confirms relay cleanup only; native process cleanup remains the execution Worker's separate responsibility.

The private transport ignores ambient proxy variables, uses system TLS verification and direct connections, disables compression, redirects, connection reuse and replayable request bodies, and does not retry. Explicit localhost endpoints dial loopback addresses directly. Outbound `HTTP-Referer` is fixed to `https://deli.dev`. No caller organization, project, cookie, proxy, destination or authorization header passes through. Anthropic uses its fixed API version and a bounded beta-identifier header.

Response checks reject literal, decoded JSON-string and Base64 representations of the upstream key and execution credential before delivery or reference persistence. A bounded linear stream matcher retains frames with unresolved protected prefixes at stable JSON string paths, preventing a credential split across native delta frames from escaping incrementally. It allows at most 1,024 active paths, 4 KiB per path and 32 MiB pending frames; overflow fails closed. This is finite reflection protection, not a claim to recognize arbitrary provider transformations or covert encodings. Never use raw provider diagnostics for logs or public errors.

## Logging
Use structured `api_proxy_request_finished` metadata: correlation/execution/session/account/provider/model IDs, closed operation and phase, stream selection, submission uncertainty, HTTP status, typed failure code and duration. `submitted` means an HTTP attempt began, not proof that upstream accepted it. Native failed terminal events retain failure classification. Keys, endpoint URLs, native model strings, prompt/output bytes and provider diagnostic text are excluded.

## Build and Test
- `go test -race ./cmds/delidev-cli/...`
- `go vet ./cmds/delidev-cli/...`

Real loopback HTTP fixtures test all five operations, unchanged native JSON/SSE, injected headers, scope escapes, native-reference authorization, secret reflection/fragmentation, bounds, cancellation, TLS verification, ignored environment proxies, dropped responses and absence of retry. Server tests additionally exercise authenticated registration/replay, durable reference ownership, canceled jobs, account revocation, server-epoch replacement, and cancellation/join before protected-key deletion. Their native-readiness/job records and provider are controlled fixtures; these tests never invoke real inference or user accounts. The Linux arm64 test binary also runs in a non-root network-disabled container. Cross-compilation for other targets does not prove native runtime acceptance. Installed macOS Codex now passes an opt-in composition through authenticated registration and this server relay to a scripted loopback provider, with server-only key injection and exact model/input/output. Its accepted readiness is simulated, and no external account inference is claimed. Actual Worker token delivery, public dispatch and real provider-account evidence remain required.

## Dependencies and Integrations
Depends on the existing provider validation and protected connection contracts. The server supplies durable authority and cancellation; the selected native adapter must supply verified operation compatibility and its own structured output. Primary native protocol references: [OpenAI Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create), [OpenAI streaming Responses](https://developers.openai.com/api/docs/guides/streaming-responses), [Anthropic Messages](https://platform.claude.com/docs/en/api/messages/create), [Anthropic streaming](https://platform.claude.com/docs/en/build-with-claude/streaming), and [Anthropic token counting](https://platform.claude.com/docs/en/api/messages/count_tokens).

## Change Triggers
Update the project index, CLI contract, evidence ledger and scoped AGENTS with authority/lifecycle integration or changes to routes, bounds, secret handling, native references or supported protocols. Add exact native acceptance evidence before marking a harness/provider combination supported. Preserve the complete issue requirements.

## References
- [Project](project-delidev.md)
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)
