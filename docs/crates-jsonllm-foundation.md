# jsonllm Rust foundation

## Scope

The `jsonllm` project owns `crates/jsonllm-shared`, `crates/jsonllm-derive`, and `crates/jsonllm`.

## Runtime and Language

Rust libraries and a procedural macro; generation uses the asynchronous clairic-client transport.

## Users and Operators

Rust developers extracting typed values with Cloudflare Workers AI credentials; maintainers publishing the three crates.

## Interfaces and Contracts

- `JsonGen` derive accepts nonempty named structs and preserves generics and where clauses. It implements the `JsonGen` trait without requiring a serde derive.
- `JsonGen::fields` builds Clairic fields. `JsonGen::from_response` rejects truncated responses, missing answers, null required values, and invalid value types.
- `JsonGen::generate(&client, state)` sends one request. `JsonGen::generate_with(&client, request)` preserves images, model, reference date and options, and replaces request fields with the type's schema.
- `String`, Rust integer and float primitives, `Option<T>`, and scalar `Vec<T>` use `JsonField`. Lists support text and numbers. Optional null answers become `None`; missing answers remain errors.
- `JsonField` and `ListItem` allow explicit custom field mappings. Unsupported types, including bool, nested objects, and nested lists, have no automatic mapping because Clairic has no matching field kind.
- Field doc comments become instructions. `#[jsonllm(rename = "name", instructions = "description")]` overrides the wire name and instructions. Duplicate or invalid names and unsupported attributes fail compilation. Serde attributes do not control this independent mapping.
- `Error` distinguishes upstream, truncated, missing-field and invalid-field failures. Invalid-field errors retain the field name and serde source; display text omits response values.
- The facade re-exports the macro, shared traits, Clairic client/types and serde_json for custom decoders. Renamed facade dependencies work.

## Storage

Requests and responses remain in memory. No persistent storage.

## Security

The caller supplies the configured client and credentials. Transport validation, TLS policy, timeouts and error details remain owned by clairic-client. Generation does not retry billable calls or coerce malformed answers. Upstream error details can contain sensitive data and must not be logged without filtering.

## Logging

Use tracing for field counts and generation outcomes. Do not log credentials, prompts, images or answer values. Install no subscriber.

## Build and Test

Run `cargo test -p jsonllm-shared -p jsonllm-derive -p jsonllm` and root `cargo test`. Cover derive diagnostics, schema mapping, decoding failures and loopback HTTP integration without live credentials. Inspect package contents and licenses. Package verification is not live Cloudflare acceptance.

## Dependencies and Integrations

The facade's `examples/invoice.rs` performs one live `Invoice::generate` call using `CLOUDFLARE_ACCOUNT_ID` and `CLOUDFLARE_API_TOKEN`. It checks a fixed public invoice's string, number, list and optional-null fields and exits unsuccessfully on configuration, generation or value mismatch. It prints only the verified sample and sanitized errors, without credentials or raw upstream debug output. Its README documents token creation, account scoping, execution, expected output, troubleshooting and cleanup. Live inference is an explicit manual check, separate from credential-free Cargo tests; it does not retry billable requests.

Use clairic-client 0.1, serde_json, serde, tracing, thiserror, syn, quote and proc-macro-crate. Runtime depends on shared and derive; shared has no macro dependency.

Publish with `cargo mono publish --package jsonllm-shared --package jsonllm-derive --package jsonllm`. The selected packages are ordered by their dependencies. First publication requires registry credentials and available crate names; dependent dry runs need the dependencies published or a local registry fixture.

## Change Triggers

Update this contract and the [project index](project-jsonllm.md) for ownership or API changes. Keep workspace release metadata and crate dependency versions synchronized.

## References

- [Project index](project-jsonllm.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
