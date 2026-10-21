use clairic_client::{Client, Field, FieldType, Request};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = Client::new(
        &std::env::var("CLOUDFLARE_ACCOUNT_ID")?,
        &std::env::var("CLOUDFLARE_API_TOKEN")?,
    )?;
    let request = Request::new("Invoice A-42. Total USD 125.50.")
        .with_field("invoice", Field::new(FieldType::Text))
        .with_field("total", Field::new(FieldType::Number));
    let result = client.run(&request).await?;
    println!("{}", serde_json::to_string_pretty(&result)?);
    Ok(())
}
