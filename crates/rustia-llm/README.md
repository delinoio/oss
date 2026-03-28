# rustia-llm

`rustia-llm` provides a typed adapter from Rust `rustia` models to `aisdk::core::tools::Tool`.

The adapter enforces rustia's three-layer harness for tool input:

1. Lenient JSON parsing
2. Parse-time type coercion
3. Validation feedback

## Quick Start

```rust
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use rustia::LLMData;
use rustia_llm::{LlmToolSpec, tool};

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

## API Key Setup

`rustia-llm` only builds typed `Tool` adapters. API key configuration is handled by
the `aisdk` provider you use (for example, OpenAI).

If you use OpenAI, enable the provider feature in your app crate:

```bash
cargo add aisdk --features openai
```

### Option A: Environment Variable (recommended)

```bash
export OPENAI_API_KEY="<YOUR_OPENAI_API_KEY>"
```

Then use your tool with `aisdk`:

```rust
use aisdk::{core::LanguageModelRequest, providers::OpenAI};

let response = LanguageModelRequest::builder()
    .model(OpenAI::gpt_5())
    .prompt("Use the provided tool to answer the request.")
    .with_tool(weather_tool)
    .build()
    .generate_text()
    .await?;
```

### Option B: Explicit API Key in Provider Builder

```rust
use aisdk::providers::OpenAI;

let mut openai = OpenAI::gpt_5();
openai.settings.api_key = "<YOUR_OPENAI_API_KEY>".to_owned();
```

Use `openai` as the model in `LanguageModelRequest::builder().model(openai)`.

## API Surface

- `LlmToolInput`: `rustia::LLMData + schemars::JsonSchema + Send + Sync + 'static`
- `LlmToolOutput`: `serde::Serialize + Send + Sync + 'static`
- `LlmToolSpec`
- `tool<I, O, F, E>(spec, handler) -> Result<aisdk::core::tools::Tool, LlmToolBuildError>`

## Error Model

- `LlmToolBuildError`
- `LlmToolInputError`
- `LlmToolExecutionError`

Input parsing and validation failures return deterministic feedback text in the tool error payload, including rustia validation paths (`$input...`) and expected constraints.

## Local Validation

Run from repository root:

```bash
cargo test -p rustia-llm
cargo test --workspace --all-targets
```

## Documentation Links

- Project index: [`docs/project-rustia.md`](../../docs/project-rustia.md)
- rustia-llm contract: [`docs/crates-rustia-llm-foundation.md`](../../docs/crates-rustia-llm-foundation.md)
- rustia core contract: [`docs/crates-rustia-core-foundation.md`](../../docs/crates-rustia-core-foundation.md)
