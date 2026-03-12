# Feature: interfaces

## Interfaces
Canonical component identifiers:

```ts
enum SerdeFeatherComponent {
  Core = "core",
  Macros = "macros",
}
```

Canonical feature identifiers:

```ts
enum SerdeFeatherFeature {
  Std = "std",
  Derive = "derive",
}
```

Canonical derive macro identifiers:

```ts
enum SerdeFeatherDeriveMacro {
  FeatherSerialize = "FeatherSerialize",
  FeatherDeserialize = "FeatherDeserialize",
}
```

Package and feature contract:
- `serde-feather` default features: `["std"]`.
- `serde-feather` feature `std` maps to `serde/std`.
- `serde-feather` feature `derive` maps to optional dependency `serde-feather-macros` and requires `std`.
- `serde-feather-macros` is configured as `proc-macro = true`.
- Stable public derive macro identifiers:
  - `FeatherSerialize`
  - `FeatherDeserialize`

Phase 4 derive target and attribute contract:
- Derive target:
  - Structs: unit, tuple, named.
  - Enums: unit, newtype, tuple, named variants.
  - Generic type/lifetime/const parameter forms.
- Attribute namespace: `serde(...)` only.
- `rename_all` scope:
  - Container-level `rename_all` applies to struct named fields and enum variant names.
  - Variant-level `rename_all` applies to named variant field names.
- Enum encoding/decoding uses serde default externally tagged representation.
- Enum deserialization must accept both string variant names and numeric variant discriminants.
- Variant aliases (`alias`) are deserialization-only names.
- Unknown input fields during deserialization must be ignored.
- Struct and named-variant deserialization must support both map and sequence encodings.
- Tuple-struct and tuple-variant deserialization must support sequence encodings.
- Sequence decoding must treat `skip_deserializing` fields as omitted positions (no placeholder element is consumed).
- Unknown enum variants must fail with deterministic `unknown_variant` errors.
- Overlapping `skip`, `skip_serializing`, and `skip_deserializing` combinations must be rejected deterministically.
- Tuple-field `rename` must be rejected with compile-time errors.
- Variant/container unsupported attributes must be rejected with compile-time errors.
- `with` must be honored for both serialization and deserialization hooks.
- `skip_serializing_if` must be honored for both struct fields and enum variant fields.
- Effective wire field names must be unique in both serialization and deserialization field sets.
- Effective wire enum variant names must be unique in serialization and deserialization name sets (including aliases).
- Auto-generated generic bounds must be added only for type parameters used in active serialization/deserialization paths.
- Default wire field names must strip Rust raw identifier prefixes (for example `r#type` -> `type`).
- Unsupported shapes and unsupported `serde(...)` attributes must fail with compile-time errors at attribute/type span.

