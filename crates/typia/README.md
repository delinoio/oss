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

Current parse parity baseline for lenient behavior:

- `samchon/typia` `master` parse suite at commit
  `29a02742661d476ce5ef5414fe32acc7e97c0e6c`

Important parity exclusions:

- JS `undefined`-dependent expectations
- non-finite numbers (`Infinity`, `-Infinity`)
- lone-surrogate code-unit expectations

Additional EOF recovery contract:

- incomplete/truncated but structurally recoverable inputs are accepted by the
  lenient parser (for example: unclosed object/array/string, key-only EOF, and
  key-colon EOF), while token/syntax violations still surface failures.

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
