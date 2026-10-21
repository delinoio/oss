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

### Field instructions, names, and generics

Import [`JsonGen`] to call trait methods; the derive macro is also available as `jsonllm::JsonGen`. A generic field works when its concrete type implements [`JsonField`].

```rust
use jsonllm::{JsonGen, clairic_client::FieldType};

#[derive(jsonllm::JsonGen)]
struct Line<T> {
    /// Identifier printed beside the item.
    #[jsonllm(rename = "item-id")]
    id: String,
    /// This comment is replaced by the explicit instructions below.
    #[jsonllm(instructions = "Number of units ordered")]
    quantity: T,
}

let fields = Line::<u16>::fields();
assert_eq!(fields["item-id"].instructions.as_deref(),
    Some("Identifier printed beside the item."));
assert_eq!(fields["quantity"].kind, FieldType::Number);
assert_eq!(fields["quantity"].integer, Some(true));
assert_eq!(fields["quantity"].instructions.as_deref(),
    Some("Number of units ordered"));
```

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

The following example reads a local PNG and sends it with text and a reference date. It requires the same credentials and Tokio dependency as the first example. Image bytes are embedded in the request; remote image URLs are not supported.

```no_run
use jsonllm::{Client, JsonGen, Request, clairic_client::{Image, ImageType, Options}};

#[derive(Debug, jsonllm::JsonGen)]
struct Receipt {
    merchant: String,
    total: f64,
    purchased_on: Option<String>,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        &std::env::var("CLOUDFLARE_ACCOUNT_ID")?,
        &std::env::var("CLOUDFLARE_API_TOKEN")?,
    )?;
    let mut request = Request::new("Extract the receipt. Resolve relative dates from the reference date.");
    request.images.push(Image::from_bytes(ImageType::Png, &std::fs::read("receipt.png")?));
    request.reference_date = Some("2026-10-21".into());
    request.options = Some(Options {
        return_state_text: Some(true),
        include_quantiles: None,
    });
    let receipt = Receipt::generate_with(&client, request).await?;
    println!("{receipt:?}");
    Ok(())
}
```

### Retain usage and answer metadata

To retain usage or confidence metadata, call `client.run(&request)` yourself with `request.fields = Invoice::fields()`, then decode with `Invoice::from_response(&response)`. This example compiles without sending a request during doctests:

```no_run
use jsonllm::{Client, Error, JsonGen, Request};

#[derive(jsonllm::JsonGen)]
struct Summary {
    title: String,
}

async fn extract(client: &Client) -> Result<Summary, Error> {
    let mut request = Request::new("Document title: Quarterly report");
    request.fields = Summary::fields();
    let response = client.run(&request).await?;
    let summary = Summary::from_response(&response)?;
    println!("Output tokens: {}", response.usage.output_tokens);
    if let Some(confidence) = response.answers["title"].confidence {
        println!("Title confidence: {confidence}");
    }
    Ok(summary)
}
```

### Decode a response without a live service

You can decode stored responses or fixtures without a client, credentials, or async runtime:

```rust
use jsonllm::{Error, JsonGen, Response, serde_json::{self, json}};

#[derive(Debug, PartialEq, jsonllm::JsonGen)]
struct Record {
    name: String,
    tags: Vec<String>,
    note: Option<String>,
}

let mut response: Response = serde_json::from_value(json!({
    "model": "clairic", "truncated": false,
    "usage": {"input_tokens": 12, "output_tokens": 8},
    "answers": {
        "name": {"type": "text", "value": "Notebook"},
        "tags": {"type": "list", "value": ["stationery", "paper"]},
        "note": {"type": "text", "value": null}
    }
}))?;
let record = Record::from_response(&response)?;
assert_eq!(record.name, "Notebook");
assert_eq!(record.tags, vec!["stationery", "paper"]);
assert_eq!(record.note, None);

response.answers.remove("note");
assert!(matches!(Record::from_response(&response),
    Err(Error::MissingField { field }) if field == "note"));
response.truncated = true;
assert!(matches!(Record::from_response(&response), Err(Error::Truncated)));
# Ok::<(), Box<dyn std::error::Error>>(())
```

A truncated response is an error. Missing answer entries are errors even for optional fields. A null answer decodes to `None` only for optional fields. Invalid values return `Error::InvalidField`; values are not coerced. Extra answers are ignored. Error display and tracing omit prompts and answer values, but upstream error fields and debug output can contain sensitive data.

## Custom scalar and list elements

Implement [`JsonField`] for a local wrapper to control the field schema and validate the returned JSON value. Implement [`ListItem`] to use that wrapper in `Vec<T>`. This example uses only facade re-exports; it needs no direct serde or Clairic dependency.

```rust
use jsonllm::{Field, JsonField, JsonGen, ListItem, serde_json::{self, Value, json},
    clairic_client::{FieldType, ListItemType}};

#[derive(Debug, PartialEq)]
struct ProductCode(String);

impl JsonField for ProductCode {
    fn field() -> Field {
        Field::new(FieldType::Text).with_instructions("Product code starting with SKU-")
    }

    fn decode(value: &Value) -> Result<Self, serde_json::Error> {
        let code = String::decode(value)?;
        if !code.starts_with("SKU-") {
            return Err(serde_json::Error::io(std::io::Error::new(
                std::io::ErrorKind::InvalidData,
                "expected a product code starting with SKU-",
            )));
        }
        Ok(Self(code))
    }
}

impl ListItem for ProductCode {
    fn item_type() -> ListItemType {
        ListItemType::Text
    }
}

#[derive(jsonllm::JsonGen)]
struct Shipment {
    codes: Vec<ProductCode>,
}

assert_eq!(Shipment::fields()["codes"].items, Some(ListItemType::Text));
assert_eq!(Vec::<ProductCode>::decode(&json!(["SKU-42"]))?,
    vec![ProductCode("SKU-42".into())]);
assert!(ProductCode::decode(&json!("invalid")).is_err());
# Ok::<(), serde_json::Error>(())
```

Field doc comments or explicit `instructions` replace the instructions returned by a custom scalar's `field()`. A list schema uses the element's `item_type()`; it does not copy the element's field options. Put list extraction instructions on the struct field.

## Handle failures

Match [`Error`] variants to distinguish request failures from incomplete or invalid responses. This example handles errors without logging prompts, answers, or upstream source details:

```rust
use jsonllm::Error;

fn failure_kind(error: &Error) -> &'static str {
    match error {
        Error::Upstream(_) => "request failed",
        Error::Truncated => "response incomplete",
        Error::MissingField { .. } => "answer missing",
        Error::InvalidField { .. } => "answer invalid",
    }
}

assert_eq!(failure_kind(&Error::Truncated), "response incomplete");
```

## License

Apache-2.0. See LICENSE and NOTICE.
