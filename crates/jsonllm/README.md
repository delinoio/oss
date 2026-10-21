# jsonllm

Generate typed Rust values with Cloudflare Workers AI Clairic. Add one derive to a named struct; no serde derive is required.

```toml
[dependencies]
jsonllm = "0.1"
tokio = { version = "1", features = ["macros", "rt-multi-thread"] }
```

```no_run
use jsonllm::{Client, JsonGen};

#[derive(Debug, jsonllm::JsonGen)]
struct Invoice {
    /// Invoice identifier printed on the document.
    id: String,
    #[jsonllm(instructions = "Total amount due in USD")]
    total: f64,
    due_date: Option<String>,
    items: Vec<String>,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        &std::env::var("CLOUDFLARE_ACCOUNT_ID")?,
        &std::env::var("CLOUDFLARE_API_TOKEN")?,
    )?;
    let invoice = Invoice::generate(&client, "Invoice A-42. Two books. Total USD 50.").await?;
    println!("{invoice:?}");
    Ok(())
}
```

Clairic is an extraction model. The input should contain the information to extract. An account with Workers AI access and a Tokio runtime are required for live requests. Generation sends one request and does not retry automatically.

## Types and fields

Supported fields are `String`, Rust integer and float primitives, `Option<T>` and `Vec<T>` of strings or numbers. Type aliases and generic fields work when they implement `JsonField`. Custom scalar wrappers can implement `JsonField`; implement `ListItem` too to use them in lists. Numeric values must fit the Rust target type and satisfy serde's numeric decoding rules. Clairic numbers have JSON precision limits; large integers are not guaranteed to be exact.

The derive requires a nonempty named struct. Enums, tuple structs, bool, nested objects and nested lists have no automatic mapping to Clairic field kinds. Unsupported fields produce a trait-bound error.

Field doc comments supply extraction instructions. Override them with `#[jsonllm(instructions = "...")]`. Use `#[jsonllm(rename = "wire_name")]` to change the request/answer name. Names follow Clairic's ASCII identifier rules and must be unique. Serde attributes do not change this mapping. Raw identifiers use their unprefixed name.

These types fail at compile time:

```compile_fail
#[derive(jsonllm::JsonGen)]
struct Unsupported { enabled: bool }
```

```compile_fail
#[derive(jsonllm::JsonGen)]
enum Unsupported { First, Second }
```

## Images and response handling

Build a `jsonllm::Request`, append embedded images through `jsonllm::clairic_client::Image`, and call `Invoice::generate_with(&client, request).await`. This preserves the model, images, reference date and response options, and replaces its field definitions with `Invoice::fields()`.

To retain usage or confidence metadata, call `client.run(&request)` yourself with `request.fields = Invoice::fields()`, then decode with `Invoice::from_response(&response)`.

A truncated response is an error. Missing answer entries are errors even for optional fields. A null answer decodes to `None` only for optional fields. Invalid values return `Error::InvalidField`; values are not coerced. Extra answers are ignored. Error display and tracing omit prompts and answer values, but upstream error fields and debug output can contain sensitive data.

## License

Apache-2.0. See LICENSE and NOTICE.
