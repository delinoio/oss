# typia

> **Rust version of [typia.io](https://typia.io/).**

`typia` is the Rust version of `typia.io`, providing serde-based type-safe
validation and LLM JSON parsing utilities.

## Stable APIs

- `Validate` trait
  - `validate(value: serde_json::Value) -> IValidation<Self>`
  - `validate_equals(value: serde_json::Value) -> IValidation<Self>`
- `IValidation<T>`
- `IValidationError`
- `LLMData` trait
  - `parse(input: &str) -> LlmJsonParseResult<Self>`
  - `stringify(&self) -> Result<String, serde_json::Error>`
- `LlmJsonParseResult<T>`
- `LlmJsonParseError`
- `#[derive(LLMData)]` (from `typia-macros`, re-exported by `typia`)

## Example

```rust
use typia::{
    IValidation, LLMData, Validate,
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

    let coercion = User::parse(r#""{\"id\":2,\"name\":\"bob\"}""#);
    println!("{coercion:?}");

    let validated = User::validate(typia::serde_json::json!({ "id": 1, "name": "alice" }));
    if let IValidation::Success { data } = validated {
        println!("{}", data.stringify().unwrap());
    }
}
```

## Derive Tags

`#[derive(LLMData)]` supports typia-style field tags via `#[typia(tags(...))]`.

```rust
#[derive(Debug, Serialize, Deserialize, LLMData)]
struct Product {
    #[typia(tags(minLength(1), maxLength(20), pattern("^[a-z0-9-]+$")))]
    slug: String,
    #[typia(tags(minimum(0), maximum(100)))]
    score: i32,
    #[typia(tags(minItems(1), items(tags(minLength(2)))))]
    labels: Vec<String>,
}
```

Supported nesting tags:
- `items(tags(...))`
- `keys(tags(...))`
- `values(tags(...))`

## Lenient Parser Behaviors

`LLMData::parse()` uses typia's internal lenient JSON parser before serde validation.

`LLMData::parse()` also applies a parse-only coercion pass when validation
fails:

- follows validation error paths (`$input...`)
- reparses string values with the lenient parser
- replaces values only when reparsing succeeds and changes the JSON type/value
- retries validation up to `MAX_PARSE_COERCION_ROUNDS = 16`

This enables common LLM output recovery such as:

- stringified object -> object (for struct/object targets)
- stringified array -> array
- stringified number/boolean/null -> number/boolean/null

Direct `Validate::validate()` / `validate_equals()` calls remain strict and do
not apply this coercion.

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
