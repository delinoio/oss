# jsonllm-shared

Shared field definitions and strict response decoding for [jsonllm](https://crates.io/crates/jsonllm). Most applications should depend on jsonllm and use its re-exports.

`JsonField` maps a Rust value to a Clairic `Field` and decodes a JSON answer. `ListItem` marks supported scalar list elements. Built-in implementations cover strings, numeric primitives, optional values and scalar lists. `decode_field` reports missing entries and invalid values through `Error`.

Custom implementations can use the re-exported `clairic_client` and `serde_json` types. They must return compatible field definitions and validate their response values.

## Decode built-in field types

The following example runs without credentials or a network request:

```rust
use jsonllm_shared::{JsonField, clairic_client::FieldType, serde_json::json};

assert_eq!(String::field().kind, FieldType::Text);
assert_eq!(u16::field().integer, Some(true));
assert_eq!(Option::<u16>::decode(&json!(null))?, None);
assert_eq!(Vec::<u16>::decode(&json!([2, 3]))?, vec![2, 3]);
assert!(u16::decode(&json!(-1)).is_err());
# Ok::<(), jsonllm_shared::serde_json::Error>(())
```

See [`JsonField`] for the decoder contract, [`ListItem`] for list compatibility, and [`decode_field`] for named response decoding and error examples. `decode_field` does not check response truncation; the facade's derived `JsonGen::from_response` does.

Apache-2.0. See LICENSE and NOTICE.
