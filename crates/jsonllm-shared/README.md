# jsonllm-shared

Shared field definitions and strict response decoding for [jsonllm](https://crates.io/crates/jsonllm). Most applications should depend on jsonllm and use its re-exports.

`JsonField` maps a Rust value to a Clairic `Field` and decodes a JSON answer. `ListItem` marks supported scalar list elements. Built-in implementations cover strings, numeric primitives, optional values and scalar lists. `decode_field` reports missing entries and invalid values through `Error`.

Custom implementations can use the re-exported `clairic_client` and `serde_json` types. They must return compatible field definitions and validate their response values.

Apache-2.0. See LICENSE and NOTICE.
