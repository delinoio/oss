use std::{
    io::{Read, Write},
    net::TcpListener,
    sync::mpsc,
    thread,
    time::Duration,
};

use clairic_client::*;
use serde_json::{Value, json};

fn result() -> Value {
    json!({"model":"clairic", "answers":{
        "total":{"type":"number", "value":125.5, "confidence":0.98},
        "missing":{"type":"text", "value":null, "guess":"A-42", "p_null":1, "span":[8,12]},
        "contacts":{"type":"list", "value":["Dana"], "spans":[[0,4]]},
        "estimate":{"type":"estimate", "value":30, "mean":29.5, "median":30, "p10":21, "p90":38, "p_below":0.01, "p_above":0.02, "quantiles":[1,2]},
        "forecast":{"type":"forecast", "value":[30], "steps":[{"value":30}], "future_metadata":true}
    }, "truncated":false, "usage":{"input_tokens":10, "output_tokens":0, "passes":1}})
}

fn request() -> Request {
    Request::new("Total USD 125.50.").with_field("total", Field::new(FieldType::Number))
}

fn server(
    status: &str,
    headers: &str,
    body: String,
) -> (Client, mpsc::Receiver<String>, thread::JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let client = Client::with_endpoint(
        "test-account",
        "test-secret",
        &format!("http://{}/client/v4/", listener.local_addr().unwrap()),
        Duration::from_secs(5),
    )
    .unwrap();
    let response = format!(
        "HTTP/1.1 {status}\r\nContent-Length: {}\r\nConnection: close\r\n{headers}\r\n{body}",
        body.len()
    );
    let (tx, rx) = mpsc::channel();
    let handle = thread::spawn(move || {
        let (mut socket, _) = listener.accept().unwrap();
        socket
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        let mut bytes = vec![];
        let mut buffer = [0; 4096];
        loop {
            let count = socket.read(&mut buffer).unwrap();
            assert_ne!(count, 0);
            bytes.extend_from_slice(&buffer[..count]);
            let text = String::from_utf8_lossy(&bytes);
            if let Some((head, body)) = text.split_once("\r\n\r\n") {
                let len: usize = head
                    .lines()
                    .find_map(|line| {
                        line.to_ascii_lowercase()
                            .strip_prefix("content-length: ")
                            .map(str::to_owned)
                    })
                    .unwrap()
                    .parse()
                    .unwrap();
                if body.len() >= len {
                    break;
                }
            }
        }
        tx.send(String::from_utf8(bytes).unwrap()).unwrap();
        socket.write_all(response.as_bytes()).unwrap();
    });
    (client, rx, handle)
}

#[tokio::test]
async fn sends_rest_contract_and_decodes_envelope() {
    let (client, rx, handle) = server(
        "200 OK",
        "",
        json!({"success":true,"errors":[],"result":result()}).to_string(),
    );
    let response = client.run(&request()).await.unwrap();
    assert_eq!(response.answers["total"].value_as::<f64>().unwrap(), 125.5);
    assert_eq!(
        response.answers["missing"]
            .value_as::<Option<String>>()
            .unwrap(),
        None
    );
    assert_eq!(response.answers["contacts"].spans, Some(vec![vec![0, 4]]));
    assert_eq!(response.answers["estimate"].p90, Some(json!(38)));
    assert_eq!(response.answers["forecast"].extra["future_metadata"], true);
    let sent = rx.recv().unwrap();
    assert!(sent.starts_with(
        "POST /client/v4/accounts/test-account/ai/run/@cf/cloudflare/clairic HTTP/1.1"
    ));
    assert!(sent.contains("authorization: Bearer test-secret\r\n"));
    assert!(sent.contains("content-type: application/json\r\n"));
    let body: Value = serde_json::from_str(sent.split_once("\r\n\r\n").unwrap().1).unwrap();
    assert_eq!(
        body,
        json!({"model":"clairic","state":"Total USD 125.50.","fields":{"total":{"type":"number"}}})
    );
    handle.join().unwrap();
}

#[tokio::test]
async fn decodes_direct_result() {
    let (client, _rx, handle) = server("200 OK", "", result().to_string());
    assert!(!client.run(&request()).await.unwrap().truncated);
    handle.join().unwrap();
}

#[tokio::test]
async fn preserves_http_status_and_retry_after() {
    let (client, _rx, handle) = server(
        "429 Too Many Requests",
        "Retry-After: 12\r\n",
        "rate limited".into(),
    );
    let error = client.run(&request()).await.unwrap_err();
    assert!(!error.to_string().contains("rate limited"));
    match error {
        Error::Http {
            status,
            body,
            retry_after,
        } => {
            assert_eq!(status.as_u16(), 429);
            assert_eq!(body, "rate limited");
            assert_eq!(retry_after.as_deref(), Some("12"));
        }
        other => panic!("unexpected error: {other}"),
    }
    handle.join().unwrap();
}

#[tokio::test]
async fn handles_api_missing_and_malformed_responses() {
    for (body, expected) in [
        (
            json!({"success":false,"result":null,"errors":[{"code":10000,"message":"denied"}]})
                .to_string(),
            "api",
        ),
        (json!({"success":true,"result":null}).to_string(), "missing"),
        (
            json!({"success":true,"errors":[{"code":1}],"result":result()}).to_string(),
            "api",
        ),
        ("not json".into(), "decode"),
        ("{}".into(), "decode"),
    ] {
        let (client, _rx, handle) = server("200 OK", "", body);
        let error = client.run(&request()).await.unwrap_err();
        assert!(
            matches!(
                (&error, expected),
                (Error::Api { .. }, "api")
                    | (Error::MissingResult, "missing")
                    | (Error::Decode(_), "decode")
            ),
            "{error:?}"
        );
        handle.join().unwrap();
    }
}

#[tokio::test]
async fn refuses_redirects() {
    let (client, _rx, handle) = server(
        "302 Found",
        "Location: https://example.com/\r\n",
        String::new(),
    );
    assert!(
        matches!(client.run(&request()).await, Err(Error::Http { status, .. }) if status.as_u16() == 302)
    );
    handle.join().unwrap();
}

#[test]
fn validates_configuration_and_redacts_client() {
    let client = Client::new("private-account", "private-token").unwrap();
    let debug = format!("{client:?}");
    assert!(!debug.contains("private"));
    for base in [
        "http://example.com",
        "https://user:pass@example.com",
        "https://example.com/?x=1",
        "file:///tmp/api",
    ] {
        assert!(Client::with_endpoint("account", "token", base, Duration::from_secs(1)).is_err());
    }
    assert!(Client::new("../escape", "token").is_err());
    assert!(Client::new("account", "token\ninvalid").is_err());
    assert!(Client::new("account", "").is_err());
}

#[test]
fn serializes_all_field_types_and_options() {
    for (kind, name) in [
        (FieldType::Text, "text"),
        (FieldType::Number, "number"),
        (FieldType::Date, "date"),
        (FieldType::Time, "time"),
        (FieldType::Datetime, "datetime"),
        (FieldType::Duration, "duration"),
        (FieldType::List, "list"),
        (FieldType::Estimate, "estimate"),
        (FieldType::Forecast, "forecast"),
    ] {
        assert_eq!(
            serde_json::to_value(Field::new(kind)).unwrap(),
            json!({"type":name})
        );
    }
    let field: Field = serde_json::from_value(json!({"type":"forecast","min":0,"max":100,"step":1,"horizon":7,"interval":"day","scale":"log","instructions":"Revenue","unit":"USD","integer":false,"decimals":2,"null_threshold":0.5})).unwrap();
    let mut req = Request::new("Revenue history").with_field("revenue", field);
    req.options = Some(Options {
        include_quantiles: Some(true),
        return_state_text: Some(true),
    });
    req.reference_date = Some("2026-10-21".into());
    req.validate().unwrap();
    let value = serde_json::to_value(&req).unwrap();
    assert_eq!(value["fields"]["revenue"]["interval"], "day");
    assert_eq!(serde_json::from_value::<Request>(value).unwrap(), req);
    assert_eq!(
        serde_json::to_value(Step::Calendar(CalendarStep::Month)).unwrap(),
        "month"
    );
    assert_eq!(
        serde_json::to_value(Bound::Date("2026-10-21".into())).unwrap(),
        "2026-10-21"
    );
}

#[test]
fn rejects_invalid_fields_and_images() {
    assert!(Request::new("").validate().is_err());
    assert!(Request::new("text").validate().is_err());
    for field in [
        Field::new(FieldType::List),
        Field::new(FieldType::Estimate),
        Field::new(FieldType::Forecast),
    ] {
        assert!(
            Request::new("text")
                .with_field("x", field)
                .validate()
                .is_err()
        );
    }
    let mut req = request();
    req.fields.get_mut("total").unwrap().decimals = Some(7);
    assert!(req.validate().is_err());
    req.fields.get_mut("total").unwrap().decimals = None;
    req.fields.get_mut("total").unwrap().null_threshold = Some(f64::NAN);
    assert!(req.validate().is_err());
    let mut req = request();
    req.state.clear();
    req.images
        .push(Image::from_bytes(ImageType::Png, b"fixture"));
    req.validate().unwrap();
    req.images = vec![Image::DataUrl("data:image/png;base64,Zml4dHVyZQ==".into())];
    req.validate().unwrap();
    for url in [
        "https://example.com/image.png",
        "data:image/gif;base64,YQ==",
        "data:image/png;base64,???",
    ] {
        req.images = vec![Image::DataUrl(url.into())];
        assert!(req.validate().is_err());
    }
    req.images = vec![Image::from_bytes(ImageType::Jpeg, &[0; 1]); 5];
    assert!(req.validate().is_err());
    req.images = vec![Image::from_bytes(
        ImageType::Webp,
        &vec![0; 4 * 1024 * 1024 + 1],
    )];
    assert!(req.validate().is_err());
    req.images = vec![Image::from_bytes(ImageType::Png, &vec![0; 3 * 1024 * 1024]); 3];
    assert!(req.validate().is_err());
}

#[tokio::test]
async fn rejects_oversized_body_before_network_access() {
    let client = Client::with_endpoint(
        "account",
        "token",
        "http://127.0.0.1:1/",
        Duration::from_secs(1),
    )
    .unwrap();
    let mut req = request();
    req.state = "x".repeat(16 * 1024 * 1024);
    assert!(matches!(client.run(&req).await, Err(Error::Validation(_))));
}

#[tokio::test]
async fn reports_transport_timeout() {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let client = Client::with_endpoint(
        "account",
        "token",
        &format!("http://{}/", listener.local_addr().unwrap()),
        Duration::from_millis(50),
    )
    .unwrap();
    // Keep the listening socket alive without serving a response.
    assert!(
        matches!(client.run(&request()).await, Err(Error::Transport(error)) if error.is_timeout())
    );
}

#[test]
fn rejects_invalid_estimate_ranges() {
    let mut field = Field::new(FieldType::Estimate);
    field.min = Some(Bound::Number(0.0));
    field.max = Some(Bound::Number(10.0));
    for step in [0.0, -1.0, f64::INFINITY, f64::NAN] {
        field.step = Some(Step::Number(step));
        assert!(
            Request::new("text")
                .with_field("x", field.clone())
                .validate()
                .is_err()
        );
    }
    field.step = None;
    field.max = Some(Bound::Number(-1.0));
    assert!(
        Request::new("text")
            .with_field("x", field.clone())
            .validate()
            .is_err()
    );
    field.max = Some(Bound::Date("2026-12-01".into()));
    assert!(
        Request::new("text")
            .with_field("x", field)
            .validate()
            .is_err()
    );
}

#[tokio::test]
async fn validates_duration_units_before_network_access() {
    let client = Client::with_endpoint(
        "account",
        "token",
        "http://127.0.0.1:1/",
        Duration::from_secs(1),
    )
    .unwrap();
    for unit in ["day", "", "SECONDS", "fortnights", " seconds"] {
        let mut field = Field::new(FieldType::Duration);
        field.unit = Some(unit.into());
        let req = Request::new("Two days").with_field("duration", field);
        assert!(matches!(client.run(&req).await, Err(Error::Validation(_))));
    }
    for unit in [
        DurationUnit::Seconds,
        DurationUnit::Minutes,
        DurationUnit::Hours,
        DurationUnit::Days,
        DurationUnit::Weeks,
        DurationUnit::Months,
        DurationUnit::Years,
    ] {
        let label = unit.as_str();
        assert_eq!(DurationUnit::try_from(label).unwrap(), unit);
        assert_eq!(serde_json::to_value(unit).unwrap(), label);
        let field: Field = serde_json::from_value(json!({"type":"duration","unit":label})).unwrap();
        Request::new("Two days")
            .with_field("duration", field)
            .validate()
            .unwrap();
    }
    Request::new("Two days")
        .with_field("duration", Field::new(FieldType::Duration))
        .validate()
        .unwrap();
    let mut number = Field::new(FieldType::Number);
    number.unit = Some("USD per item".into());
    Request::new("USD 10 per item")
        .with_field("price", number)
        .validate()
        .unwrap();
}

#[tokio::test]
async fn validates_field_names_before_network_access() {
    let client = Client::with_endpoint(
        "account",
        "token",
        "http://127.0.0.1:1/",
        Duration::from_secs(1),
    )
    .unwrap();
    for name in [
        "".to_owned(),
        "a".repeat(65),
        "invoice total".into(),
        "1total".into(),
        "-total".into(),
        ".total".into(),
        "total/amount".into(),
        "total\n".into(),
        "금액".into(),
        "aé".into(),
    ] {
        let req = Request::new("Total 10").with_field(name, Field::new(FieldType::Number));
        assert!(matches!(client.run(&req).await, Err(Error::Validation(_))));
    }
    for name in [
        "a".to_owned(),
        "_".into(),
        "a".repeat(64),
        "Invoice_9.total-USD".into(),
    ] {
        let req = Request::new("Total 10").with_field(name.clone(), Field::new(FieldType::Number));
        req.validate().unwrap();
        assert!(
            serde_json::to_value(req).unwrap()["fields"]
                .get(&name)
                .is_some()
        );
    }
}
