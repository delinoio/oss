# Project: jsonllm

## Goal

Generate typed Rust values from text or images through Clairic with only `#[derive(jsonllm::JsonGen)]` on supported structs.

## Project ID

`jsonllm`

## Domain Ownership Map

- `crates/jsonllm-shared`: field mapping and response decoding contracts.
- `crates/jsonllm-derive`: derive macro expansion.
- `crates/jsonllm`: public facade and asynchronous generation.
  Its `examples/invoice.rs` is the runnable live extraction example; its README owns user setup and troubleshooting.

## Domain Contract Documents

- [Rust foundation](crates-jsonllm-foundation.md)

## Cross-Domain Invariants

- Rust is required for the library and procedural macro.
- The derive uses the facade's re-exports; consumers do not need direct shared, serde, or Clairic dependencies.
- All three crates use Apache-2.0 and registry-versioned local dependencies.
- All three packages are explicit workspace members and cargo-mono publish-tag targets, like rustia-llm.
- Publication uses explicit local `cargo mono publish`, not the manual CLI release workflow or a main-push publisher.

## Change Policy

Update the foundation contract with API or supported-type changes. Update ownership instructions when paths change. Keep workspace membership, dependency versions and publish-tag eligibility consistent.

## References

- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Rust foundation](crates-jsonllm-foundation.md)
