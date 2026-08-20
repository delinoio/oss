use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::{MAX_OUTER_HTML_BYTES, PROTOCOL_VERSION, REQUEST_DEADLINE_MILLIS, SCHEMA_VERSION};

pub const SESSION_INVALIDATED_ERROR: &str = "session-invalidated";
const MAX_BROWSER_CONTEXT_TEXT_BYTES: usize = 4 * 1024;

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum NativeMessageType {
    Pair,
    Configure,
    Capture,
    Ping,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum IpcMessageType {
    Pair,
    Configure,
    Capture,
    Ping,
    RevokePairing,
}

impl From<NativeMessageType> for IpcMessageType {
    fn from(value: NativeMessageType) -> Self {
        match value {
            NativeMessageType::Pair => Self::Pair,
            NativeMessageType::Configure => Self::Configure,
            NativeMessageType::Capture => Self::Capture,
            NativeMessageType::Ping => Self::Ping,
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct NativeRequest {
    pub version: u16,
    pub schema_version: u16,
    pub request_id: String,
    #[serde(rename = "type")]
    pub message_type: NativeMessageType,
    pub deadline_unix_ms: i64,
    pub nonce: String,
    #[serde(default)]
    pub pairing_nonce: Option<String>,
    #[serde(default)]
    pub payload: Value,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct NativeResponse {
    pub version: u16,
    pub schema_version: u16,
    pub request_id: String,
    pub ok: bool,
    pub state: NativeResponseState,
    #[serde(default)]
    pub payload: Value,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum NativeResponseState {
    Paired,
    Accepted,
    Disconnected,
    Denied,
    Malformed,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
#[serde(rename_all = "camelCase")]
pub struct BrowserContext {
    pub url: String,
    pub title: String,
    pub viewport: Viewport,
    pub user_agent: String,
    pub selected_bounds: Option<Bounds>,
    pub accessibility: std::collections::BTreeMap<String, String>,
    pub outer_html: String,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Viewport {
    pub width: f64,
    pub height: f64,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Bounds {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Challenge {
    pub version: u16,
    pub schema_version: u16,
    pub challenge_id: String,
    pub challenge: String,
    pub deadline_unix_ms: i64,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum AuthPurpose {
    BrowserSession,
    PairingRevocation,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct AuthResponse {
    pub version: u16,
    pub schema_version: u16,
    pub challenge_id: String,
    pub extension_id: String,
    pub origin: String,
    pub client_nonce: String,
    pub pairing_nonce: Option<String>,
    pub purpose: AuthPurpose,
    pub proof: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct AuthResult {
    pub version: u16,
    pub schema_version: u16,
    pub accepted: bool,
    pub session_id: Option<String>,
    #[serde(default)]
    pub proof: Option<String>,
    pub error: Option<String>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct IpcRequest {
    pub version: u16,
    pub schema_version: u16,
    pub request_id: String,
    #[serde(rename = "type")]
    pub message_type: IpcMessageType,
    pub issued_at_unix_ms: i64,
    pub deadline_unix_ms: i64,
    pub nonce: String,
    pub payload: Value,
    pub proof: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct IpcResponse {
    pub version: u16,
    pub schema_version: u16,
    pub request_id: String,
    pub accepted: bool,
    pub error: Option<String>,
    #[serde(default)]
    pub payload: Value,
}

pub fn validate_version(version: u16, schema_version: u16) -> Result<(), &'static str> {
    if version != PROTOCOL_VERSION || schema_version != SCHEMA_VERSION {
        return Err("unsupported-version");
    }
    Ok(())
}

pub fn validate_deadline(issued_at: i64, deadline: i64, now: i64) -> Result<(), &'static str> {
    if issued_at > now || deadline < now || deadline - issued_at > REQUEST_DEADLINE_MILLIS {
        return Err("expired-request");
    }
    Ok(())
}

pub fn validate_browser_context(context: &BrowserContext) -> Result<(), &'static str> {
    if context.outer_html.len() > MAX_OUTER_HTML_BYTES
        || context.title.len() > MAX_BROWSER_CONTEXT_TEXT_BYTES
        || context.user_agent.len() > MAX_BROWSER_CONTEXT_TEXT_BYTES
        || context
            .accessibility
            .values()
            .any(|value| value.len() > MAX_BROWSER_CONTEXT_TEXT_BYTES)
        || validate_outer_html(&context.outer_html).is_err()
        || !context.viewport.width.is_finite()
        || !context.viewport.height.is_finite()
        || context.viewport.width <= 0.0
        || context.viewport.height <= 0.0
        || context.selected_bounds.as_ref().is_some_and(|bounds| {
            !bounds.x.is_finite()
                || !bounds.y.is_finite()
                || !bounds.width.is_finite()
                || !bounds.height.is_finite()
                || bounds.width <= 0.0
                || bounds.height <= 0.0
        })
    {
        return Err("invalid-browser-context");
    }
    let url = url::Url::parse(&context.url).map_err(|_| "invalid-browser-context")?;
    if !matches!(url.scheme(), "http" | "https")
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || url.host_str().is_none()
        || url
            .path()
            .split('/')
            .any(|segment| !segment.is_empty() && !segment.eq_ignore_ascii_case("%3Credacted%3E"))
        || context.accessibility.keys().any(|key| {
            !matches!(
                key.as_str(),
                "alt"
                    | "aria-describedby"
                    | "aria-hidden"
                    | "aria-label"
                    | "aria-labelledby"
                    | "role"
                    | "title"
            )
        })
    {
        return Err("invalid-browser-context");
    }
    Ok(())
}

fn allowed_element(name: &[u8]) -> bool {
    matches!(
        name,
        b"a" | b"article"
            | b"aside"
            | b"blockquote"
            | b"code"
            | b"dd"
            | b"details"
            | b"div"
            | b"dl"
            | b"dt"
            | b"em"
            | b"figcaption"
            | b"figure"
            | b"footer"
            | b"h1"
            | b"h2"
            | b"h3"
            | b"h4"
            | b"h5"
            | b"h6"
            | b"header"
            | b"hr"
            | b"img"
            | b"li"
            | b"main"
            | b"nav"
            | b"ol"
            | b"p"
            | b"pre"
            | b"section"
            | b"summary"
            | b"table"
            | b"tbody"
            | b"td"
            | b"tfoot"
            | b"th"
            | b"thead"
            | b"tr"
            | b"ul"
    )
}

#[cfg(test)]
mod message_type_tests {
    use super::*;

    #[test]
    fn pairing_revocation_is_available_only_on_the_app_ipc_envelope() {
        let native = serde_json::json!({
            "version": 1,
            "schema_version": 1,
            "request_id": "01900000-0000-7000-8000-000000000000",
            "type": "revoke-pairing",
            "deadline_unix_ms": 1,
            "nonce": "nonce",
            "payload": null
        });
        assert!(serde_json::from_value::<NativeRequest>(native).is_err());

        let ipc = serde_json::json!({
            "version": 1,
            "schema_version": 1,
            "request_id": "01900000-0000-7000-8000-000000000000",
            "type": "revoke-pairing",
            "issued_at_unix_ms": 0,
            "deadline_unix_ms": 1,
            "nonce": "nonce",
            "payload": null,
            "proof": "proof"
        });
        assert_eq!(
            serde_json::from_value::<IpcRequest>(ipc)
                .expect("IPC revocation request")
                .message_type,
            IpcMessageType::RevokePairing
        );
    }
}

fn allowed_attribute(name: &[u8]) -> bool {
    matches!(
        name,
        b"alt"
            | b"aria-describedby"
            | b"aria-hidden"
            | b"aria-label"
            | b"aria-labelledby"
            | b"role"
            | b"title"
    )
}

fn validate_html_element(
    element: &quick_xml::events::BytesStart<'_>,
) -> Result<Vec<u8>, &'static str> {
    let name = element.name().as_ref().to_vec();
    if !allowed_element(&name) {
        return Err("invalid-browser-context");
    }
    for attribute in element.attributes() {
        let attribute = attribute.map_err(|_| "invalid-browser-context")?;
        if !allowed_attribute(attribute.key.as_ref())
            || (attribute.key.as_ref() == b"aria-hidden"
                && attribute.value.as_ref().eq_ignore_ascii_case(b"true"))
        {
            return Err("invalid-browser-context");
        }
    }
    Ok(name)
}

fn validate_outer_html(value: &str) -> Result<(), &'static str> {
    use quick_xml::{Reader, events::Event};
    let mut reader = Reader::from_str(value);
    // HTML void elements have no end tag, so nesting is checked by the explicit
    // stack below rather than quick-xml's XML-only last-start check.
    reader.config_mut().check_end_names = false;
    let mut stack: Vec<Vec<u8>> = Vec::new();
    loop {
        match reader.read_event() {
            Ok(Event::Start(element)) => {
                let name = validate_html_element(&element)?;
                if !matches!(name.as_slice(), b"hr" | b"img") {
                    stack.push(name);
                }
            }
            Ok(Event::Empty(element)) => {
                validate_html_element(&element)?;
            }
            Ok(Event::End(element)) => {
                if stack.pop().as_deref() != Some(element.name().as_ref()) {
                    return Err("invalid-browser-context");
                }
            }
            Ok(Event::Text(_) | Event::CData(_) | Event::GeneralRef(_)) => {}
            Ok(Event::Eof) if stack.is_empty() => return Ok(()),
            Ok(Event::Eof) | Ok(_) | Err(_) => return Err("invalid-browser-context"),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn deadline_is_bounded_to_five_seconds() {
        assert_eq!(validate_deadline(10_000, 15_000, 12_000), Ok(()));
        assert_eq!(
            validate_deadline(10_000, 15_001, 12_000),
            Err("expired-request")
        );
        assert_eq!(
            validate_deadline(10_000, 15_000, 15_001),
            Err("expired-request")
        );
    }

    #[test]
    fn browser_context_rejects_extra_or_sensitive_url_data() {
        let invalid = r#"{"url":"https://example.com/?token=x","title":"x","viewport":{"width":1,"height":1},"userAgent":"x","selectedBounds":null,"accessibility":{},"outerHtml":""}"#;
        let context: BrowserContext = serde_json::from_str(invalid).unwrap();
        assert_eq!(
            validate_browser_context(&context),
            Err("invalid-browser-context")
        );
        let extra = invalid.replace(
            "\"title\":\"x\"",
            "\"title\":\"x\",\"selector\":\"#secret\"",
        );
        assert!(serde_json::from_str::<BrowserContext>(&extra).is_err());
        let raw_path = invalid.replace(
            "https://example.com/?token=x",
            "https://example.com/private",
        );
        let context: BrowserContext = serde_json::from_str(&raw_path).unwrap();
        assert_eq!(
            validate_browser_context(&context),
            Err("invalid-browser-context")
        );
        let unsafe_html = invalid
            .replace(
                "https://example.com/?token=x",
                "https://example.com/%3Credacted%3E",
            )
            .replace(
                "\"outerHtml\":\"\"",
                "\"outerHtml\":\"<a href='https://secret'>x</a>\"",
            );
        let context: BrowserContext = serde_json::from_str(&unsafe_html).unwrap();
        assert_eq!(
            validate_browser_context(&context),
            Err("invalid-browser-context")
        );
    }

    #[test]
    fn browser_context_accepts_only_allowlisted_redacted_markup() {
        let value = r#"{"url":"https://example.com/%3Credacted%3E","title":"safe","viewport":{"width":1280,"height":720},"userAgent":"browser","selectedBounds":{"x":0,"y":0,"width":10,"height":10},"accessibility":{"aria-label":"safe"},"outerHtml":"<main aria-label='safe'><p>A &amp; B</p><img alt='safe'></main>"}"#;
        let context: BrowserContext = serde_json::from_str(value).unwrap();
        assert_eq!(validate_browser_context(&context), Ok(()));
    }

    #[test]
    fn browser_context_enforces_text_field_utf8_byte_limits() {
        let value = r#"{"url":"https://example.com/%3Credacted%3E","title":"safe","viewport":{"width":1280,"height":720},"userAgent":"browser","selectedBounds":null,"accessibility":{"aria-label":"safe"},"outerHtml":""}"#;
        let context: BrowserContext = serde_json::from_str(value).unwrap();
        let boundary = "é".repeat(MAX_BROWSER_CONTEXT_TEXT_BYTES / 2);
        let oversized = format!("{boundary}x");

        let mut bounded = context.clone();
        bounded.title = boundary.clone();
        bounded.user_agent = boundary.clone();
        bounded
            .accessibility
            .insert("aria-label".to_string(), boundary);
        assert_eq!(validate_browser_context(&bounded), Ok(()));

        let mut oversized_title = context.clone();
        oversized_title.title = oversized.clone();
        assert_eq!(
            validate_browser_context(&oversized_title),
            Err("invalid-browser-context")
        );

        let mut oversized_user_agent = context.clone();
        oversized_user_agent.user_agent = oversized.clone();
        assert_eq!(
            validate_browser_context(&oversized_user_agent),
            Err("invalid-browser-context")
        );

        let mut oversized_accessibility = context;
        oversized_accessibility
            .accessibility
            .insert("aria-label".to_string(), oversized);
        assert_eq!(
            validate_browser_context(&oversized_accessibility),
            Err("invalid-browser-context")
        );
    }
}
