# Compatibility and verification

## What compatibility means

The compatibility target is opencodex revision `88aaa754566d119b6b8fc898f8fb47602387d82e`. Later upstream changes are not automatically included. This is a reference target, not a claim that TaiRouter has implemented or tested every route, provider, or client.

| Surface                  | Required coverage                                           | Status communicated by this guide                                     |
| ------------------------ | ----------------------------------------------------------- | --------------------------------------------------------------------- |
| Responses                | HTTP, WebSocket, compaction, streaming, tools, continuation | Complete compatibility verification unavailable                       |
| Chat Completions         | Requests, responses, streams, errors, model selection       | Complete compatibility verification unavailable                       |
| Anthropic Messages       | Messages and token counting                                 | Complete compatibility verification unavailable                       |
| Models and hosted search | Discovery and provider-specific behavior                    | Complete compatibility verification unavailable                       |
| Images                   | Generation, editing, temporary artifact retrieval           | Complete compatibility verification unavailable                       |
| Providers                | API keys, OAuth, pools, routing, retry/timeout exceptions   | Per-provider live acceptance not established                          |
| Client configuration     | Formats, paths, platform constraints, restoration, relay    | Configuration handlers implemented; native acceptance not established |

This table is an overview, not an exhaustive inventory or list of supported providers. A release needs a complete interface/provider/authentication/client matrix with evidence for each included path and an explicit exclusion for each omitted path.

Hosted video-helper output uses `video/mp4` with a 200 MiB per-video and 600 MiB per-turn limit. It shares the 200-file per-organization/user quota, 24-hour expiry, and at-most-five-minute signed URLs with images. Image limits remain 50 MiB per image and 100 MiB decoded per response. There is no public `/v1/videos` API. See [R2 media guidance](configuration.md); live media acceptance is not claimed.

## Implemented workflows and remaining limits

The console implements account and organization workflows, provider/pool/routing editors, keys, usage views, and operator controls. The CLI implements browser device approval, protected credentials, named profiles, client configuration/restoration, and loopback service control. [Client guidance](clients.md) lists the current handlers, including native Codex catalog/cache configuration and Claude gateway/Desktop profiles.

The Compose packaging includes the console and API together, PostgreSQL, optional monitoring, proxy examples, and offline maintenance. Automated fixture evidence exists for parts of these workflows. It does not establish production image execution, live identity/provider accounts, native credential stores, or real application compatibility. A final source-revision acceptance assessment remains separate.

Provider OAuth/refresh and advanced pool/routing selection are implemented, with automated fixtures rather than live provider acceptance. Complete provider-specific retry, quota, and discovery parity is not implied. Usage filtering/aggregation still requires its final service verification. Keep unavailable errors visible for unsupported provider methods or missing evidence. Optional [native TLS compatibility](native-compatibility.md) has separate build gates, consent, and cleanup requirements; the native command/runtime tests do not establish real OS trust or installed-application acceptance. The account-UI trial is implemented with independent personal-account checks, separate consent, and a three-minute maximum; these safeguards and automated tests do not establish live native-account acceptance.

[Cursor Private Inference setup](clients.md#cursor-private-inference-manual-gateway-setup) is manual URL/key/model guidance. It is separate from the Cursor network provider and does not include automatic client mutation, installation, or real-app acceptance. Its reasoning/context controls depend on the installed client as well as gateway metadata.

The console exposes [native OAuth pool and fast policy controls](providers.md#native-oauth-pools), plus JEV used-quota tiers. Their presence does not establish live quota/credit availability or faster execution. Native pooling requires a supported homogeneous OAuth pool; optional values retain their documented default, zero, and disabled meanings. Fast discovery and execution require actual supported capability, not only an enabled override.

## Explicit differences

TaiRouter isolates personal credentials and routing by both organization and user. It requires OIDC, scoped inference keys, bounded encrypted temporary state, and R2 media expiry. Revocation, removal, cancellation, and database outages stop additional sends. Historical cost estimates retain the price used for the request. Local program execution as a provider and associated execution-agent features are excluded.

HTTP local model servers with operator approval, client configuration, and loopback relays remain in scope. User export and automatic import of settings, credentials, and usage are excluded.

## Interpret verification evidence

- A documentation build checks publication output. It does not verify login, inference, restore, or deployment.
- A binary or container build checks packaging for a target. It does not prove native credential storage or background-service behavior.
- Mock and contract tests check controlled responses and required rules. They do not prove a real account, OAuth consent, current provider API behavior, or a production proxy.
- Integration and deployment fixtures check their stated environment. Record its versions and limitations.
- Live provider/account, actual native OS, and real deployment checks require separate evidence. None is claimed by these guides.

Release evidence should identify the tested version, commands, results, and unperformed checks without secrets or raw user content. Required automated gates include security, isolation, recovery, protocol/integration, packaging, and reproducible load tests. Live acceptance is separate from those first-release gates.

## Performance

There is no fixed throughput, latency, availability, or SLO guarantee. Interpret load results only with the workload, provider mocks or accounts, hardware/resources, concurrency, latency distribution, and errors used for that run. A mocked load result does not measure live provider capacity.
