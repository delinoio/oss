# typia

`typia` provides serde-based LLM JSON utilities for Rust.

## Stable APIs

- `LLMData` trait
  - `parse(input: &str) -> LlmJsonParseResult<Self>`
  - `validate(value: serde_json::Value) -> Result<Self, serde_json::Error>`
  - `stringify(&self) -> Result<String, serde_json::Error>`
- `LlmJsonParseResult<T>`
- `LlmJsonParseError`
- `#[derive(LLMData)]` (from `typia-macros`, re-exported by `typia`)

## Example

```rust
use typia::{
    LLMData,
    serde::{Deserialize, Serialize},
};

#[derive(Debug, Serialize, Deserialize, LLMData)]
struct User {
    id: u32,
    name: String,
}

fn main() {
    let parsed = User::parse("{id: 1, name: \"alice\",}");
    println!("{parsed:?}");
}
```

## Lenient Parser Behaviors

`LLMData::parse()` uses typia's internal lenient JSON parser before serde validation.

Supported recovery behaviors:

- markdown ` ```json ... ``` ` extraction
- junk prefix skipping before JSON payloads
- JavaScript comments (`//`, `/* ... */`)
- unquoted object keys
- trailing commas
- partial keywords (`tru`, `fal`, `nul`)
- unclosed strings/brackets with partial recovery
- unicode escapes (including surrogate-pair decoding)
- depth guard (`MAX_DEPTH = 512`)

## Local Validation

Run from repository root:

```bash
cargo test -p typia
cargo test --workspace --all-targets
```

## Documentation Links

- Project index: [`docs/project-typia.md`](../../docs/project-typia.md)
- Core contract: [`docs/crates-typia-core-foundation.md`](../../docs/crates-typia-core-foundation.md)
- Macros contract: [`docs/crates-typia-macros-foundation.md`](../../docs/crates-typia-macros-foundation.md)
