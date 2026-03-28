# typia-llm

`typia-llm` provides a typed adapter from Rust `typia` models to `aisdk::core::tools::Tool`.

The adapter enforces typia's three-layer harness for tool input:

1. Lenient JSON parsing
2. Parse-time type coercion
3. Validation feedback

## Quick Start

```rust
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use typia::LLMData;
use typia_llm::{LlmToolSpec, tool};

#[derive(Debug, Serialize, Deserialize, JsonSchema, LLMData)]
struct WeatherInput {
    city: String,
    days: u8,
}

#[derive(Debug, Serialize, Deserialize)]
struct WeatherOutput {
    summary: String,
}

fn main() {
    let weather_tool = tool::<WeatherInput, WeatherOutput, _, std::convert::Infallible>(
        LlmToolSpec::new(
            "get_weather",
            "Get a short weather summary for a city and forecast length",
        ),
        |input| {
            Ok(WeatherOutput {
                summary: format!("{} forecast for {} day(s)", input.city, input.days),
            })
        },
    )
    .expect("tool should build");

    let output = weather_tool
        .execute
        .call(serde_json::json!({ "city": "seoul", "days": "2" }))
        .expect("tool execution should succeed");

    println!("{output}");
}
```

## API Surface

- `LlmToolInput`: `typia::LLMData + schemars::JsonSchema + Send + Sync + 'static`
- `LlmToolOutput`: `serde::Serialize + Send + Sync + 'static`
- `LlmToolSpec`
- `tool<I, O, F, E>(spec, handler) -> Result<aisdk::core::tools::Tool, LlmToolBuildError>`

## Error Model

- `LlmToolBuildError`
- `LlmToolInputError`
- `LlmToolExecutionError`

Input parsing and validation failures return deterministic feedback text in the tool error payload, including typia validation paths (`$input...`) and expected constraints.

## Local Validation

Run from repository root:

```bash
cargo test -p typia-llm
cargo test --workspace --all-targets
```

## Documentation Links

- Project index: [`docs/project-typia.md`](../../docs/project-typia.md)
- typia-llm contract: [`docs/crates-typia-llm-foundation.md`](../../docs/crates-typia-llm-foundation.md)
- typia core contract: [`docs/crates-typia-core-foundation.md`](../../docs/crates-typia-core-foundation.md)
