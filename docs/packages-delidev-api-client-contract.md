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

### Session Git comparisons

`SessionQuery.readSessionWorkspace` also carries the closed `git-diff` query/result described in the [workspace read contract](cmds-delidev-files-contract.md). Clients validate repository/comparison/path ownership, exact Git object and revision strings, bounded inert patch text and separate untracked paths; they retain no persistent file cache. The existing generated Connect service surface is unchanged.


## Local review coordinates

Generated `SessionQuery.readSessionReviewContext` exposes the exact original Worker diff and validated file/line coordinates. Preserve original side numbers, hunk identity, final-newline absence and non-line file-only states. The Go domain remains coordinate authority; clients cannot turn these read-only observations into implicit mutation or execution. Follow the [workspace contract](cmds-delidev-files-contract.md).


## Durable local review client

Generated `SessionQuery` includes comment creation/edit/deletion and grouped submission. Use `ResourceQuery` for scoped review reads. Preserve bigint entity revisions and decimal-string content revisions without numeric rounding, strict comment/submission document variants, original anchor/context and immutable submitted copies. Current data cannot replace selected request revisions or uncertain wire requests. The desktop uses connection-scoped mutation retention and does not infer agent execution from queue acceptance. Follow [local reviews](cmds-delidev-files-contract.md).


`IntegrationQuery` exports generated SaveIntegrationProfile, ReplaceIntegrationToken, ValidateIntegrationProfile and DeleteIntegrationProfile descriptors. PATs are bounded write-only fields; no token belongs in query keys, resource documents or read responses. Preserve pending-operation decimal revisions and actor-bound exact request identities under the [integration contract](cmds-delidev-integrations-contract.md).

`IntegrationQuery.inspectRepositoryIntegration` exposes the repository-specific read. Consumers must validate the complete schema-version-1 document, exact repository/revision/profile and canonical feature states before display. Preserve unknown access and previous observations distinctly; no endpoint success constitutes CI/rules/reviewer evidence. Desktop cancels/discards observations on close/inactivation and uses no automatic focus/reconnect retries.

`IntegrationQuery.queryRepositoryIntegration` supplies PR/issue list/search/detail and PR-only immutable diff, latest head Checks and combined commit-status reads. Consumers validate repository/revision/profile, exact requested query, original API identity namespace and complete bounded response. Keep nullable mergeability, independent search incompleteness/count/cap and original pagination semantics. PR observations require exactly one original detail and its exclusive selected family, matching heads, exact IDs/counts and original known/unknown states. Checks/statuses remain separate and cannot imply required CI or evaluated-commit evidence. Inactive content queries are canceled/discarded; no browser-side GitHub fetch or stored PAT is added.

`SessionQuery.linkSessionPullRequest` and `unlinkSessionPullRequest` manage durable session PR metadata. ResourceQuery reads the session-scoped `PULL_REQUEST` records. Preserve exact decimal remote identities and uint64 association revisions, session/project ownership, original uncertain request bytes and current-state receipt semantics; links never grant execution or current GitHub access. The generated messages/descriptors reproduce from the canonical schema without handwritten transport code.

Generated IntegrationService descriptors expose `GetGitHubTokenForm` and `GitHubTokenAccess`. Clients retain exact bigint profile revisions, validate the closed returned form against the selected profile and explicit scope, and separate the read from optional local OS presentation. No URL is a token-access grant.

The same repository query adds PR-only `rules`: consumers validate a complete exclusive base/head-bound active-rule inventory, exact ruleset/App IDs, unknown source states and bounded required-check entries. The desktop offers an explicit detail read and disposes inactive queries. No new transport or generated fields are required; rules do not establish evaluated-commit or CI outcomes.

PR-only `ci` uses the same generated repository query with no caller paging. Validate complete head/test-merge inventories, exclusive family, PR binding, exact App/ruleset/result identities and assessment references before display. Unknown/pending/missing and non-failing are distinct from terminal failure. The read creates no remediation execution or stored PAT and uses no browser GitHub API.

PR-only `feedback` uses the existing generated repository query without caller paging. Validate the exclusive current-PR-bound inventory, exact IDs, published states, complete review/thread membership, structural content versions and bounded inert author/body/code context. Discard inactive observations; do not treat approved/dismissed states or Bot names as local handling, GitHub App or reviewer-permission proof. No client-side GitHub request or generated transport change is introduced.
