# clairic-client

An unofficial async Rust client for [Cloudflare Workers AI Clairic](https://developers.cloudflare.com/workers-ai/models/clairic/). Clairic is a preview extraction model. This library supports text, images, lists, estimates, and forecasts through the REST API.

## Installation

```toml
[dependencies]
clairic-client = "0.1"
tokio = { version = "1", features = ["macros", "rt-multi-thread"] }
```

Version 0.1.0 is prepared for publication; registry availability is not implied by this README.

## Usage

Supply a Cloudflare account ID and an API token with Workers AI access. The library does not read environment variables itself.

```no_run
use clairic_client::{Client, Field, FieldType, Request};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        &std::env::var("CLOUDFLARE_ACCOUNT_ID")?,
        &std::env::var("CLOUDFLARE_API_TOKEN")?,
    )?;
    let request = Request::new("Invoice A-42. Total USD 125.50.")
        .with_field("invoice", Field::new(FieldType::Text))
        .with_field("total", Field::new(FieldType::Number)
            .with_instructions("Amount due in USD"));
    let response = client.run(&request).await?;
    let total: Option<f64> = response.answers["total"].value_as()?;
    println!("Total: {total:?}");
    Ok(())
}
```

`Client` is cloneable and reuses connections. Its default timeout is 120 seconds. `Client::with_endpoint` accepts a trusted API base URL and a timeout. Redirects are disabled. HTTPS is required, except for loopback HTTP endpoints used in tests. Requests are never retried automatically because inference can be billable.

## Field definitions and images

Field names must match `^[A-Za-z_][A-Za-z0-9_.\-]{0,63}$`: 1–64 ASCII characters, beginning with a letter or underscore, followed by letters, digits, underscores, periods, or hyphens. Invalid names are rejected before network access. `FieldType` covers `Text`, `Number`, `Date`, `Time`, `Datetime`, `Duration`, `List`, `Estimate`, and `Forecast`. Set public optional fields to configure units, precision, null thresholds, bounds, grid steps, scales, horizons, and intervals. For duration fields, set `unit` with `DurationUnit::Days.as_str().to_owned()` (or another enum variant). Only seconds, minutes, hours, days, weeks, months, and years are accepted; omitting the unit uses the server default of seconds. Numeric fields still accept arbitrary unit labels such as `USD`. List fields need `items`; estimates and forecasts need `min` and `max`.

```rust
use clairic_client::{Bound, Field, FieldType, Image, ImageType, ListItemType, Request, Step};

let mut contacts = Field::new(FieldType::List);
contacts.items = Some(ListItemType::Text);
let mut payment_days = Field::new(FieldType::Estimate);
payment_days.min = Some(Bound::Number(0.0));
payment_days.max = Some(Bound::Number(120.0));
payment_days.step = Some(Step::Number(1.0));
let request = Request::new("Payment is expected within 30 days.")
    .with_field("contacts", contacts)
    .with_field("payment_days", payment_days);
request.validate()?;
// For real PNG bytes, append Image::from_bytes(ImageType::Png, &bytes)
// to request.images. Image::DataUrl accepts embedded base64 data URLs.
# Ok::<(), clairic_client::Error>(())
```

Images can be PNG, JPEG, or WebP: at most four images, 4 MiB per image, 8 MiB combined decoded bytes, and a 16 MiB JSON request. Remote URLs are not supported. Image encoding and size are checked locally; actual image contents are checked by the server. Local validation covers structural constraints, not every server-side semantic rule or date format. `reference_date` anchors dates; `Options` can request quantiles and the text used by source spans.

## Responses and errors

`Response` exposes answers, truncation, state text, and usage. `Answer` retains `value`, `guess`, confidence, null probability, source spans, distribution summaries, quantiles, forecast steps, and unknown metadata. Null values stay null. Use `value_as::<Option<T>>()` when missing information is valid. Shapes left unspecified by the upstream schema remain `serde_json::Value`, including forecast step contents. Source spans are upstream character offsets, not Rust UTF-8 byte indexes.

`Error` separates validation, transport, JSON decoding, HTTP status failures, and Cloudflare API failures. HTTP errors retain the response body and `Retry-After`. API errors retain structured upstream details. Error display text omits raw server bodies; their fields and debug output can contain sensitive data. Do not log them without filtering. The client emits only counts and HTTP status through `tracing` and installs no subscriber.

Both REST success envelopes and direct model result objects are supported. A null result or an unsuccessful envelope is an error. Model output and confidence are predictions; callers decide how to use them.

## Publishing

The crate has registry-only dependencies and includes its license and notice. Maintainers can run `cargo publish --dry-run` from the package directory, then `cargo publish` after reviewing the artifact and obtaining registry credentials. A dry run does not publish a version or verify live Cloudflare access.

## License

Apache-2.0. See LICENSE and NOTICE.
