# servers-internal-foundation

## Scope
- Repository-shared Go package boundary: `servers/internal`
- Consumer in issue #722: `servers/delibase`
- Ownership: shared repository infrastructure; no independent project ID and no ownership transfer to an unrelated project.

## Runtime and Language
- Runtime: Go packages imported by server projects.
- Primary language: Go.
- Packages are reusable, narrowly scoped, and must not contain delibase-specific business rules, persistence models, billing policy, or product UI concerns.

## Users and Operators
- Server projects, initially delibase, that need consistent auth, transport, identifiers, logging, and HTTP behavior.
- Repository maintainers reviewing shared compatibility and security changes.

## Interfaces and Contracts
- Provide reusable boundaries for Logto JWT/JWKS validation, typed claims, Connect interceptors, request/trace IDs, authorization-header/forwarded-token redaction, HTTP defaults, structured logging hooks, and UUID v7 generation.
- Shared interfaces must remain provider-agnostic where possible; delibase maps them to its organization, team, meter, and billing policy.
- No package in this boundary may decide organization roles, team inheritance, Polar billing, ledger mutations, or catalog authorization.

### Package Map
- `servers/internal/auth`: canonical Logto audience, typed user/M2M claims, JWT validation, authentication context, injectable clocks, and remote/injectable JWKS sources.
- `servers/internal/authmiddleware`: HTTP middleware and unary/streaming Connect interceptors for public, user, M2M, and M2M-plus-forwarded-user requirements.
- `servers/internal/requestmeta`: safe `X-Request-Id` and `X-Trace-Id` propagation, W3C `Traceparent` trace-ID extraction, generated UUID v7 fallbacks, context access, HTTP middleware, and Connect interceptors.
- `servers/internal/safeerr`: stable error classes and safe HTTP/Connect mappings that discard arbitrary source messages and error chains.
- `servers/internal/httpserver`: exact-origin CORS middleware, handler deadlines, and conservative `http.Server` defaults.
- `servers/internal/redact`: header, string, error, log-attribute, and recursive diagnostic redaction.
- `servers/internal/safelog`: allowlisted `log/slog` events, keyed actor pseudonyms, and a defense-in-depth redacting handler.
- `servers/internal/uuidv7`: RFC 9562 UUID v7 generation with injectable clock/random sources, same-millisecond monotonic ordering, and timestamp decoding.

### Authentication Contract
- `auth.NewValidator` requires an exact Logto issuer, a `KeySource`, and the canonical `https://delibase.deli.dev` audience. Construction rejects any other audience.
- Validation requires an expiration claim, exact issuer/audience match, an allowed asymmetric algorithm (`RS256` by default), a non-empty `kid`, and a JWT header type of `JWT` or `at+jwt` by default. Clocks, leeway, algorithms, and accepted header types are injectable/configurable.
- Logto scopes are a required-subset check supplied per route. Scope success authenticates the token only; it never grants a local organization role, team access, meter allowlist, billing permission, or reservation decision.
- Every HTTP path and Connect procedure must select an explicit authentication mode. The zero-value requirement is invalid, public access requires `ModePublic`, and missing policy entries fail closed as internal configuration errors.
- A Logto M2M principal must use the client-credentials shape: `sub` equals non-empty `client_id`, with `gty=client_credentials` accepted as the explicit signal. A user principal has a distinct non-empty user `sub` and client ID. User validation rejects M2M claims and M2M validation rejects user claims.
- `token_use`, when present, must be `access_token`. ID tokens, malformed/opaque tokens, missing subjects/client IDs, and mismatched token types fail closed.
- Typed claims expose only issuer, subject, audience, expiry, issued-at, JWT ID, client ID, scopes, and user/service identity. They deliberately contain no delibase-owned organization, membership, role, team, billing, or catalog authorization state.

### JWKS Contract
- `auth.JWKS` accepts an injected `HTTPClient`, clock, cache TTL, maximum stale duration, and maximum response size. `KeySource` remains independently injectable for unit tests or non-HTTP sources.
- The configured JWKS URL and the final response URL after redirects must be HTTPS and contain no user information, query, or fragment. The default client timeout is five seconds, default cache TTL is 15 minutes, and default response limit is 1 MiB.
- Cached keys are reused within the TTL. An unknown `kid` forces one immediate refresh even while the cache is fresh so normal Logto signing-key rotation can converge promptly.
- A successful refresh atomically replaces the key set. Duplicate key IDs, empty signing sets, unsupported key types/curves, algorithm/key mismatches, oversized/invalid responses, and unknown keys fail closed.
- Stale use is disabled by default. A consumer may explicitly configure a bounded `MaxStale` window for already-known public keys during a provider outage; an unknown key is never accepted from stale state.

### Transport Contract
- `authmiddleware` removes `Authorization`, `Proxy-Authorization`, and `X-Delibase-Forwarded-User-Token` before an application handler runs. Validated claims, not raw credentials, are attached to context.
- Usage routes use `ModeM2MWithForwardedUser`: the bearer token authenticates the service and the raw dedicated header authenticates the end user. Both may have route-specific Logto scopes. Delibase must still enforce service-to-meter allowlists, organization membership, and effective team access.
- Public routes do not turn supplied credentials into identity context and still strip credential headers.
- `requestmeta` accepts only bounded, printable request IDs and 32-lowercase-hex trace IDs; malformed inbound values are replaced. Response headers carry the effective IDs for both HTTP and Connect calls, unary and streaming Connect errors carry the same IDs as vetted metadata, and `requestmeta.Propagate` plus the Connect client path copy only those IDs into downstream requests.
- `safeerr` returns only stable classes/messages. Authentication maps to HTTP 401; Connect authentication maps to `Unauthenticated` with the matching stable bearer or forwarded-user `delibase.v1.ErrorDetail.reason`. Signing-key unavailability maps to HTTP 503/Connect `Unavailable` so provider outages remain retryable while validation fails closed. Authorization maps to 403/`PermissionDenied`; unexpected errors and panics map to a generic internal response without retaining arbitrary source text. Intentional Connect errors retain their exact status code and only a recognized `delibase.v1.ErrorDetail.reason`; arbitrary response metadata, unrecognized protobuf details, and every free-form detail field are discarded. The outer `requestmeta` interceptor adds validated request and trace IDs after safe mapping.
- Compose Connect server interceptors with `requestmeta.Interceptor` outermost, then `safeerr.Interceptor`, then `authmiddleware.ConnectInterceptor`. This preserves safe IDs on successful and failed responses while ensuring authentication and application failures pass through safe mapping.

### HTTP Defaults
- Default timeouts are: read-header 5 seconds, read 15 seconds, write/handler 30 seconds, and idle 2 minutes, with a 1 MiB maximum header block. `httpserver.Server` applies the normalized handler deadline as well as the transport fields. These are operational safety defaults, not an SLO. A partial `Defaults` override retains the baseline for every zero or negative field.
- Default CORS allows exactly `https://deli.dev`, `GET`, `POST`, and `OPTIONS`, Connect browser headers including `Connect-Protocol-Version`, `Connect-Timeout-Ms`, and `X-User-Agent`, the required auth/correlation headers, and no credentialed cookies or wildcard origin. Consumers may provide an explicit list of exact HTTPS origins.

## Storage
- Shared packages are stateless by default and own no database tables, migrations, caches, or secret persistence.
- UUID v7 generation provides identifiers for consumers; persistence and transaction semantics remain owned by each service.

## Security
- Validate and type identity claims without logging tokens or raw sensitive claims. Redact authorization headers and dedicated forwarded-user headers before logs or diagnostics.
- Shared HTTP/Connect defaults must fail closed for malformed credentials and preserve context for authorization/audit decisions.
- Logto remains the identity trust boundary; shared code does not turn authentication into application authorization.
- Configure the `safelog.Pseudonymizer` with a distinct high-entropy key of at least 32 bytes. The resulting `actor:v1:<32-lowercase-hex>` value is the only Logto-user representation permitted in routine structured logs; malformed or directly constructed values are omitted, and the key and raw subject are never logged.
- The root service logger must wrap its handler with `safelog.NewRedactingHandler`. The handler recursively redacts string-keyed maps, arrays, and slices in addition to credential/secret/token keys, authorization values, JWT shapes, raw email addresses, and card-number shapes. This is defense in depth and does not authorize free-form sensitive logging.

## Logging
- Expose structured `log/slog` hooks and request/trace correlation fields without requiring a product-specific event schema.
- Shared diagnostics must support safe error classification and never include secret values, token contents, or raw billing PII.
- `safelog.Record` is the shared allowlisted event surface. It supports safe request method/procedure, request/trace IDs, pseudonymous actor, organization, team, service, meter, reservation, decision, result, and error-classification fields.
- Log events do not accept arbitrary error values, headers, URLs, request/response bodies, billing contact fields, payment/card fields, or token values. Unsafe identifier shapes are omitted.

## Build and Test
- Validate with `gofmt`, `go vet ./servers/...`, and `go test ./servers/...` when Go implementation exists.
- Add focused tests for JWT/JWKS claims, header redaction, interceptor behavior, UUID v7 shape/order, HTTP defaults, and structured logging safety.
- Any consumer must run its own tests; shared changes must not be validated only through delibase.
- Current focused validation is `go test ./servers/internal/...`; repository server validation remains `go vet ./servers/...` and `go test ./servers/...`.

## Dependencies and Integrations
- Consumed by `servers/delibase`; future server consumers require an explicit ownership and compatibility review.
- Coordinates with `protos/delibase/v1` for transport metadata but does not own Protobuf sources.
- Configuration ownership: shared packages own safe defaults and typed configuration contracts; each consuming service owns provider endpoints, credentials, and product-specific policy.

## Configuration
- Non-secret delibase configuration supplied to shared packages:
  - `DELIBASE_LOGTO_ISSUER`: exact Logto OIDC issuer.
  - `DELIBASE_LOGTO_AUDIENCE`: must equal `https://delibase.deli.dev`.
  - `DELIBASE_LOGTO_JWKS_URL`: HTTPS JWKS endpoint without credentials, query, or fragment.
  - `DELIBASE_CORS_ALLOWED_ORIGINS`: explicit exact HTTPS browser origins; production includes only `https://deli.dev`.
- Secret delibase configuration supplied to shared packages:
  - `DELIBASE_LOG_PSEUDONYM_KEY`: distinct high-entropy key of at least 32 bytes used only for log actor pseudonyms.
- Logto M2M client secrets, Polar credentials, database URLs, webhook secrets, JWTs, authorization headers, forwarded user tokens, and the pseudonym key must never be passed to logging or diagnostics.
- Consumer configuration loading must fail closed when required values are absent or malformed. Shared constructors do not read environment variables directly, which keeps configuration ownership and tests explicit.

## Change Triggers
- Update this document, `servers/AGENTS.md`, [project-delibase](project-delibase.md), and the delibase server contract for shared API, security, logging, identifier, or HTTP behavior changes.
- Update every consuming project/domain contract and run its validation when a shared exported interface changes.
- A later decision to make `servers/internal` a separately owned project requires an explicit new project ID and synchronized ownership-map change; do not infer that from a package import.

## References
- [Project delibase](project-delibase.md)
- [Server contract](servers-delibase-server-foundation.md)
- [Protobuf API contract](protos-delibase-api-contract.md)
- [Repository defaults](repository-defaults.md)
- [Issue #722](https://github.com/delinoio/oss/issues/722)
