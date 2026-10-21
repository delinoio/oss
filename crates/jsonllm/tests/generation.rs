use std::time::Duration;

use jsonllm::{
    Client, Error, JsonGen, Request, Response,
    clairic_client::{FieldType, ListItemType},
    serde_json::{self, json},
};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

#[derive(Debug, PartialEq, jsonllm::JsonGen)]
struct Invoice<T>
where
    T: PartialEq,
{
    /// Identifier on the invoice.
    #[jsonllm(rename = "invoice-id")]
    id: String,
    #[jsonllm(instructions = "Amount in USD")]
    total: T,
    count: u8,
    due: Option<String>,
    items: Vec<String>,
    amounts: Vec<f64>,
    r#type: String,
}

fn response() -> Response {
    serde_json::from_value(json!({
        "model": "clairic", "truncated": false,
        "usage": { "input_tokens": 1, "output_tokens": 1 },
        "answers": {
            "invoice-id": {"type":"text", "value":"A-42"},
            "total": {"type":"number", "value":50.5},
            "count": {"type":"number", "value":2},
            "due": {"type":"text", "value":null},
            "items": {"type":"list", "value":["book"]},
            "amounts": {"type":"list", "value":[50.5]},
            "type": {"type":"text", "value":"invoice"}
        }
    }))
    .unwrap()
}

#[test]
fn maps_fields_and_decodes_without_serde_derive() {
    let fields = Invoice::<f64>::fields();
    assert_eq!(
        fields["invoice-id"].instructions.as_deref(),
        Some("Identifier on the invoice.")
    );
    assert_eq!(
        fields["total"].instructions.as_deref(),
        Some("Amount in USD")
    );
    assert_eq!(fields["total"].kind, FieldType::Number);
    assert_eq!(fields["count"].integer, Some(true));
    assert_eq!(fields["items"].items, Some(ListItemType::Text));
    assert_eq!(fields["amounts"].items, Some(ListItemType::Number));
    assert_eq!(
        Invoice::<f64>::from_response(&response()).unwrap(),
        Invoice {
            id: "A-42".into(),
            total: 50.5,
            count: 2,
            due: None,
            items: vec!["book".into()],
            amounts: vec![50.5],
            r#type: "invoice".into(),
        }
    );
}

#[test]
fn fails_closed_on_incomplete_or_invalid_answers() {
    let mut data = response();
    data.truncated = true;
    assert!(matches!(
        Invoice::<f64>::from_response(&data),
        Err(Error::Truncated)
    ));
    data = response();
    data.answers.remove("due");
    assert!(
        matches!(Invoice::<f64>::from_response(&data), Err(Error::MissingField { field }) if field == "due")
    );
    for (field, value) in [
        ("count", json!(256)),
        ("count", json!(-1)),
        ("count", json!(1.5)),
        ("invoice-id", json!(null)),
        ("total", json!("50")),
        ("items", json!([5])),
        ("amounts", json!([null])),
    ] {
        data = response();
        data.answers.get_mut(field).unwrap().value = value;
        assert!(
            matches!(Invoice::<f64>::from_response(&data), Err(Error::InvalidField { field: name, .. }) if name == field)
        );
    }
}

async fn server(
    status: &str,
    body: String,
) -> (Client, tokio::task::JoinHandle<serde_json::Value>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let client = Client::with_endpoint(
        "account",
        "test-token",
        &format!("http://{}/client/v4/", listener.local_addr().unwrap()),
        Duration::from_secs(5),
    )
    .unwrap();
    let status = status.to_owned();
    let task = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.unwrap();
        let mut bytes = Vec::new();
        let end = loop {
            let mut chunk = [0; 4096];
            let n = socket.read(&mut chunk).await.unwrap();
            assert!(n > 0);
            bytes.extend_from_slice(&chunk[..n]);
            if let Some(i) = bytes.windows(4).position(|s| s == b"\r\n\r\n") {
                break i + 4;
            }
        };
        let header = String::from_utf8_lossy(&bytes[..end]);
        assert!(
            header.starts_with("POST /client/v4/accounts/account/ai/run/@cf/cloudflare/clairic ")
        );
        let len: usize = header
            .lines()
            .find_map(|line| {
                line.to_ascii_lowercase()
                    .strip_prefix("content-length: ")
                    .map(str::to_owned)
            })
            .unwrap()
            .parse()
            .unwrap();
        while bytes.len() < end + len {
            let mut chunk = [0; 4096];
            let n = socket.read(&mut chunk).await.unwrap();
            assert!(n > 0);
            bytes.extend_from_slice(&chunk[..n]);
        }
        let request = serde_json::from_slice(&bytes[end..end + len]).unwrap();
        socket
            .write_all(
                format!(
                    "HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: \
                     {}\r\nConnection: close\r\n\r\n{body}",
                    body.len()
                )
                .as_bytes(),
            )
            .await
            .unwrap();
        request
    });
    (client, task)
}

#[tokio::test]
async fn generates_through_clairic_and_preserves_options() {
    let body = json!({"success":true, "errors":[], "result":response()}).to_string();
    let (client, task) = server("200 OK", body).await;
    let mut request = Request::new("Invoice A-42, total 50.5");
    request.reference_date = Some("2026-10-21".into());
    request.options = Some(jsonllm::clairic_client::Options {
        return_state_text: Some(true),
        include_quantiles: None,
    });
    request
        .images
        .push(jsonllm::clairic_client::Image::from_bytes(
            jsonllm::clairic_client::ImageType::Png,
            b"fixture",
        ));
    request
        .fields
        .insert("replaced".into(), jsonllm::Field::new(FieldType::Text));
    let invoice = Invoice::<f64>::generate_with(&client, request)
        .await
        .unwrap();
    assert_eq!(invoice.total, 50.5);
    let sent = task.await.unwrap();
    assert_eq!(sent["reference_date"], "2026-10-21");
    assert_eq!(sent["options"]["return_state_text"], true);
    assert_eq!(sent["images"].as_array().unwrap().len(), 1);
    assert_eq!(sent["fields"].as_object().unwrap().len(), 7);
    assert!(sent["fields"].get("replaced").is_none());
}

#[tokio::test]
async fn propagates_http_and_api_errors() {
    for (status, body) in [
        ("429 Too Many Requests", "{}"),
        ("200 OK", r#"{"success":false,"errors":[{"code":1}]}"#),
    ] {
        let (client, task) = server(status, body.into()).await;
        assert!(matches!(
            Invoice::<f64>::generate(&client, "input").await,
            Err(Error::Upstream(_))
        ));
        task.await.unwrap();
    }
}
