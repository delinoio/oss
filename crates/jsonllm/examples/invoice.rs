use std::{env, process::ExitCode};

use jsonllm::{Client, JsonGen};

const SAMPLE: &str = "Invoice A-42. Total amount due: USD 50.50. Items: book, pen. No purchase \
                      order number was provided.";

#[derive(Debug, PartialEq, jsonllm::JsonGen)]
struct Invoice {
    /// Invoice identifier, exactly as written.
    id: String,
    /// Total amount due in USD.
    total: f64,
    /// Item names in the order listed, without quantities or punctuation.
    items: Vec<String>,
    /// Purchase order number, or null when none is provided.
    purchase_order: Option<String>,
}

fn credential(name: &str) -> Result<String, String> {
    env::var(name)
        .ok()
        .filter(|value| !value.trim().is_empty())
        .ok_or_else(|| format!("Set {name} before running this example. See the jsonllm README."))
}

async fn run() -> Result<(), String> {
    let account = credential("CLOUDFLARE_ACCOUNT_ID")?;
    let token = credential("CLOUDFLARE_API_TOKEN")?;
    let client = Client::new(&account, &token).map_err(|_| {
        "Could not configure the Clairic client. Check your credentials.".to_owned()
    })?;

    eprintln!("Sending one sample invoice to Cloudflare Workers AI Clairic...");
    let invoice = Invoice::generate(&client, SAMPLE).await.map_err(|error| {
        // Use Display only: upstream Debug output can contain raw response
        // data.
        let message = match error {
            jsonllm::Error::Upstream(source) => source.to_string(),
            other => other.to_string(),
        };
        format!("Generation failed: {message}. See README troubleshooting.")
    })?;
    let expected = Invoice {
        id: "A-42".into(),
        total: 50.5,
        items: vec!["book".into(), "pen".into()],
        purchase_order: None,
    };
    if invoice != expected {
        return Err("Extraction did not match the sample invoice. No retry was sent.".into());
    }

    // These values come only from the fixed, public sample above.
    println!("{invoice:#?}");
    println!("PASS: all four extracted fields match the sample invoice.");
    Ok(())
}

#[tokio::main]
async fn main() -> ExitCode {
    match run().await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("{error}");
            ExitCode::FAILURE
        }
    }
}
