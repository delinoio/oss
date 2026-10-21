# Clairic Client Rust Foundation

## Scope

`crates/clairic-client` owns the independently publishable `clairic-client` library.

## Runtime and Language

Rust 2024 with an asynchronous reqwest transport on Tokio. All dependencies use registry versions. Package metadata, licenses, and examples must work outside this workspace.

## Users and Operators

Rust application developers supply a Cloudflare account ID and a Workers AI API token. Maintainers verify the package before separately publishing it to crates.io.

## Interfaces and Contracts

The client posts JSON to `/client/v4/accounts/{account_id}/ai/run/@cf/cloudflare/clairic`. Requests use the `clairic` model selector and support all nine documented field types, field options, embedded images, reference dates, and inference options. Stable selectors use enums. Duration units use `DurationUnit`: seconds, minutes, hours, days, weeks, months, or years. Validation rejects other duration labels while preserving arbitrary unit labels for numeric fields. Request validation rejects missing input or fields, missing list items or estimate bounds, invalid numeric ranges, and invalid or oversized embedded images before network access.

Responses preserve null values, guesses, confidence, spans, distribution metadata, forecasts, truncation, usage, and unknown answer metadata. Schema-unspecified values and forecast steps remain JSON with typed deserialization helpers instead of assuming an undocumented shape. Both Cloudflare REST envelopes and direct model objects are accepted. HTTP failures, API failures, transport failures, and invalid response JSON remain distinct errors. HTTP errors preserve status and retry-after metadata. The client does not retry billable inference automatically.

## Storage

No persistence or caching. Images are embedded in requests; the upstream endpoint does not accept remote image URLs. No R2 storage is needed for this transport library.

## Security

Credentials stay in memory and sensitive authorization headers. Client debug output and tracing omit credentials, account identifiers, state, images, and raw responses. Default transport disables redirects and uses a finite timeout. Custom endpoints are explicitly trusted by callers; HTTPS is required except for loopback HTTP tests. Server error bodies may contain user input and must not be logged indiscriminately.

## Logging

Use structured tracing for request field/image counts and response status only. Do not install a global subscriber in the library.

## Build and Test

Run `cargo test` from the repository root, targeted `cargo test -p clairic-client`, `cargo clippy -p clairic-client --all-targets -- -D warnings`, and `cargo fmt -p clairic-client --check`. Test JSON contracts, local HTTP requests, error envelopes, malformed responses, image limits, and credential redaction. Run `cargo publish -p clairic-client --dry-run` and verify the archive builds independently. Package checks do not establish live Cloudflare acceptance or crates.io publication. Actual publication is a separate maintainer action using `cargo publish -p clairic-client`.

## Dependencies and Integrations

Registry-only reqwest, serde, serde_json, base64, thiserror, and tracing. Tokio is used by examples and tests. The Cloudflare model remains a preview upstream API.

## Change Triggers

Update this contract and public package documentation when supported schema, transport, or publication behavior changes. Update the project index and scoped instructions for ownership changes.

## References

- [Project index](project-clairic-client.md)
- [Repository defaults](repository-defaults.md)
- [License contract](repository-license-contract.md)
- [Model API](https://developers.cloudflare.com/workers-ai/models/clairic/)
