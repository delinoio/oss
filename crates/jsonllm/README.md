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

## Run the live Cargo example

The [`invoice` example](examples/invoice.rs) sends one fixed sample invoice to
`@cf/cloudflare/clairic`. It exercises `String`, `f64`, `Vec<String>` and
`Option<String>` through `Invoice::generate`. It checks every decoded value
before reporting success. You need Rust with edition 2024 support, network
access, and a Cloudflare account with Workers AI access. Clairic is currently
[in preview](https://developers.cloudflare.com/workers-ai/models/clairic/).
Live calls consume your account's Workers AI quota and can incur charges.

### Get an API token and account ID

1. Sign in to the [Cloudflare dashboard](https://dash.cloudflare.com/) and select the account to use.
2. Open **AI > Workers AI**, then select **Use REST API**.
3. Select **Create a Workers AI API Token**, review the permissions and account, and select **Create API Token**. Copy the token when it is displayed.
4. Copy **Account ID** from the same REST API setup page. Use the account covered by the token.

If you create a custom token, grant **Account > Workers AI > Read** and
**Account > Workers AI > Edit**, scoped to the selected account. Do not grant
unrelated permissions. These steps follow Cloudflare's
[REST API setup guide](https://developers.cloudflare.com/workers-ai/get-started/rest-api/).
Use a short expiration for a temporary test token.

### Execute and check the result

Clone this repository and open its root directory. Set the account ID and enter
the token at a hidden prompt. For Bash or Zsh:

```sh
export CLOUDFLARE_ACCOUNT_ID='your-account-id'
printf 'Cloudflare API token: '
read -r -s CLOUDFLARE_API_TOKEN
printf '\n'
export CLOUDFLARE_API_TOKEN
cargo run -p jsonllm --example invoice
```

Do not paste the token into command arguments, tracked files, screenshots or
issue reports. The example reads environment variables; it does not load `.env`
files. An unset, empty or whitespace-only variable fails before any API call.

A successful run exits with status 0 and prints:

```text
Invoice {
    id: "A-42",
    total: 50.5,
    items: [
        "book",
        "pen",
    ],
    purchase_order: None,
}
PASS: all four extracted fields match the sample invoice.
```

The example sends one request, uses the client's 120-second timeout and does
not retry. It exits with status 1 on configuration errors, API failures,
invalid or incomplete answers, or a mismatch with the sample values. A mismatch
means the live extraction did not pass, even if Cloudflare accepted the request.
If you change the sample, update its field instructions and expected values too.

### Troubleshooting and cleanup

- **Set CLOUDFLARE\_...**: set both variables in the shell that runs Cargo.
- **Could not configure the Clairic client**: check the account ID and token format. Supply the token alone, without a `Bearer ` prefix.
- **HTTP 401 or 403**: check token expiration, both Workers AI permissions, account scope and Workers AI access.
- **HTTP 429**: check account quota and rate limits before running again.
- **Transport, HTTP 5xx or extraction failure**: check connectivity and model availability. Clairic is in preview. A later manual run sends another billable request.

Errors use display text only. Do not add debug logging of upstream errors;
those errors can contain raw response data.

After testing, remove the variables and revoke the temporary token in the
Cloudflare dashboard's API token settings:

```sh
unset CLOUDFLARE_API_TOKEN CLOUDFLARE_ACCOUNT_ID
```

Credential-free tests use local HTTP fixtures and do not establish live account
acceptance:

```sh
cargo test -p jsonllm-shared -p jsonllm-derive -p jsonllm
```

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
