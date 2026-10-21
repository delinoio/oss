# jsonllm-derive

The `JsonGen` procedural macro for [jsonllm](https://crates.io/crates/jsonllm). Depend on jsonllm and use `#[derive(jsonllm::JsonGen)]`.

The macro supports nonempty named structs, generic fields and where clauses. Field doc comments supply instructions; `#[jsonllm(rename = "name", instructions = "description")]` overrides names and instructions. It rejects unsupported shapes, unknown options, duplicate names and invalid Clairic identifiers. Field types must implement `jsonllm::JsonField`.

Apache-2.0. See LICENSE and NOTICE.
