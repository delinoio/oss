# DeliDev TypeScript client

## Scope
`packages/delidev-api-client` owns private `@delinoio/delidev-api-client`, generated messages and service-specific Connect Query namespaces, explicit transport, typed errors, UUID-v7 request identities and bounded resource synchronization. This is the client integration boundary for desktop implementation; it does not itself constitute a desktop app or complete issue #964.

## Runtime and Language
TypeScript ES2022 modules run in the desktop renderer. Development uses the repository Node/pnpm versions and TypeScript compiler. Business rules and durable authority remain in Go. There is no browser product deployment or Rust product proxy.

## Users and Operators
The DeliDev desktop client connects to one explicitly selected authenticated local/remote server. Maintainers generate bindings from the canonical protobuf and validate them against the actual Go server.

## Interfaces and Contracts
- `buf.gen.yaml` generates messages and Connect Query descriptors exclusively from `delidev.v1`. Root generation/freshness and Turbo input/output tracking include the package. Existing Go/DevHud/ach output remains reproducible.
- `UsageQuery` exports generated summary/current-price/historical-price descriptors and the explicit price mutation. Preserve nullable decimal rates, exact amount/counter strings, separate currencies and token-basis versus native coverage. Only the server computes historical estimates; no client-side aggregation or inferred actual cost.
- `createDeliDevTransport` takes an explicit server origin and caller-owned fresh token supplier. It uses binary Connect POST, server streaming and no automatic mutation retries. `newRequestId` creates UUID-v7 mutation identities; callers retain the complete original request on uncertain retries.
- `clientFailure` preserves versioned Go problem classifications, guidance and valid correlation IDs; untyped browser/proxy errors never disclose raw exception contents.
- `synchronizeResources` is a read-only async iterator over one resource-kind/session/project scope. It atomically replaces the consumer's scope with a coherent complete server snapshot, then emits indexed current resources or removals. It never substitutes partial lists for snapshots. The existing server snapshot limit is 200 resources and its byte budget; larger histories require a separately paginated presentation and cannot be claimed complete by this helper.
- Consumers apply each update before requesting another. Only afterward does the helper commit its opaque event cursor. Duplicate events, older revisions and unrelated kinds do not reload complete history. A failed indexed read keeps the preceding cursor; NotFound after an update removes the now-deleted resource. Project reassignment removes resources outside the scope.
- Typed expired/gap cursors trigger a fresh coherent snapshot. Transient failures preserve displayed state and reconnect with capped exponential backoff and jitter; authentication/revocation, compatibility, invalid evidence and capacity errors stop with an explicit problem. Disconnection cannot imply completion or dispatch elsewhere. Signals cancel reads, streaming and retry waits, suppressing late publication.
- The helper bounds live resource metadata/document accounting, defaults to 1,000 resources and 4 MiB, permits at most 10,000 resources/16 MiB, and retains at most 512 event IDs. It holds no growing transcript copy or retry queue. Native notification deduplication, paginated large-history presentation and desktop lifecycle remain separate required work.

Additional explicit `watchKinds` may share the primary kind snapshot cursor, loading only changed identities of those kinds. Their initial state must be paginated separately; they are never invented as members of the primary snapshot. All watched kinds share the memory/revision bounds.

## Storage
No implicit persistence. Tokens remain caller-owned; cursors, resource revision/size accounting and duplicate IDs exist only during the selected connection. No SQLite, workspace, account browser or secure-vault access is implemented here. A client switches server/device only after canceling prior streams and clearing their query/resource caches.

## Security
HTTP is restricted to literal `127.0.0.1`, `[::1]` or `localhost`; remote origins require HTTPS. Raw authority validation precedes WHATWG normalization, rejecting numeric IPv4 aliases, credentials, paths, queries and fragments. Redirects, browser cookies and HTTP caching are disabled. No auth discovery/fallback, secret logging, Web Storage, Tauri traffic proxy or harness/provider endpoint access occurs. Query keys must never contain credentials. RPC authorization and all product mutations remain server-enforced.

## Logging
The library does not log resource documents, credentials, cursors, exception objects or request bodies. It exposes closed connection states and typed safe failures with correlation IDs so the consuming desktop can report actionable status. Actual server-side RPC logs retain their existing sanitized boundary.

## Build and Test
- `pnpm --filter @delinoio/delidev-api-client lint`, `test`, and `build` validate types, synchronization/transport fixtures and generated exports.
- The test command compiles and starts the real Go server in a private temporary scope, exercises binary RPC, coherent snapshots, revision events, idempotent mutation replay, indexed deletion and typed authentication failure, then stops its owned process. It never reads user credentials, invokes inference, or starts a Worker. Go is required; the integration task is uncached.
- Run root protocol formatting/lint/compatibility and repeat generation without drift. The existing shared protocol CI job runs client validation and is selected by DeliDev command/client inputs. Generated `dist` is an explicit compilation output and is removed from the final worktree.

## Dependencies and Integrations
Pinned Buf protobuf 2.14.0, Connect/Connect Web 2.1.2 and Connect Query 2.3.1 follow repository versions. React Query integration uses generated service namespaces; no second product transport or client-side routing/eligibility engine is introduced. The Go server and its canonical resource schemas remain authoritative.

## Change Triggers
Update generated bindings, package/domain AGENTS, protocol contract, project index, evidence ledger, generation freshness/Turbo outputs and CI when schemas, origin/auth boundaries, synchronization behavior or validation commands change.

## References
- [Project](project-delidev.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Connect Web](https://connectrpc.com/docs/web/getting-started/)
- [Connect interceptors](https://connectrpc.com/docs/web/interceptors/)

SessionQuery exposes generated budget read/write descriptors and the client recognizes the typed `budget_reached` failure. Budget amounts remain exact decimal strings and counts retain uint64 precision. Clients never recompute price history, decide execution eligibility or reinterpret incomplete evidence as verified compliance; the owner/client session RPC remains authoritative.

The existing resource/event envelope also carries optional typed Claude message documents from Go. One provider message remains one indexed message resource containing ordered block indices and independent block/message lifecycle. Clients must preserve the complete document, fetch revisions by original resource identity, and never flatten blocks or infer a terminal execution from provider message closure. No new transport or native API path is introduced.

The existing `Resource.document_json` usage shape may contain a typed `claude_observation`. Preserve nullable decimal-string counters and exact native USD estimate strings, independent provider reports and input-result main-loop/cumulative scopes. It does not change generated schemas or grant billing/response-ledger authority.

### Session files

Generated `SessionQuery.readSessionWorkspace` exposes the [file explorer contract](cmds-delidev-files-contract.md). Presentation keeps decimal size strings exact and uses bounded, nonpersistent caches without mutation intents, background polling or automatic retries. File text stays inert; clients cannot supply absolute roots or turn native transcript paths into access authority.

## Portable configuration
Generated `ConfigurationQuery` exposes export, preview and apply. Clients preserve the original document bytes, explicit target mappings, exact reviewed plan and retained request identity; JavaScript numeric parsing is never the serialization authority. Preview editing invalidates apply, and accepted jobs are observed independently of mutation retries. Follow [portable configuration](cmds-delidev-configuration-transfer-contract.md).
