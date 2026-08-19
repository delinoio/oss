use std::{
    io,
    sync::{
        Arc, Mutex, OnceLock,
        atomic::{AtomicU64, Ordering},
    },
    time::{Duration, Instant},
};

use devhud_native_messaging_host::{
    PROTOCOL_VERSION, SCHEMA_VERSION,
    auth::{
        ReplayGuard, new_challenge, now_unix_millis, random_nonce, verify_handshake, verify_request,
    },
    clear_pairing_complete, configured_extension_id, delete_pairing_secret, endpoint,
    expected_extension_origin,
    framing::{ByteOrder, read_json, write_json},
    generate_pairing_secret, mark_pairing_complete, pairing_is_complete,
    protocol::{
        AuthResponse, AuthResult, BrowserContext, IpcRequest, IpcResponse, NativeMessageType,
        validate_browser_context, validate_deadline, validate_version,
    },
    read_pairing_secret, write_pairing_secret,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use tracing::{error, info, warn};

const PAIRING_NONCE_TTL: Duration = Duration::from_secs(120);
const CONNECTION_TIMEOUT: Duration = Duration::from_secs(5);
const REPLAY_LIMIT: usize = 4_096;

static STATE: OnceLock<Arc<NativeMessagingState>> = OnceLock::new();

pub fn register_packaged_host() -> Result<(), String> {
    use std::process::Command;

    use wait_timeout::ChildExt;
    let current = std::env::current_exe().map_err(|error| error.to_string())?;
    let name = if cfg!(windows) {
        "devhud-native-messaging-host.exe"
    } else {
        "devhud-native-messaging-host"
    };
    let binary = current
        .parent()
        .ok_or("application executable has no parent")?
        .join(name);
    if !binary.is_file() {
        return Err("packaged Native Messaging host was not found".to_string());
    }
    let mut child = Command::new(&binary)
        .arg("register")
        .arg(&binary)
        .spawn()
        .map_err(|error| error.to_string())?;
    match child
        .wait_timeout(CONNECTION_TIMEOUT)
        .map_err(|error| error.to_string())?
    {
        Some(status) if status.success() => Ok(()),
        Some(_) => Err("Native Messaging host registration failed".to_string()),
        None => {
            let _ = child.kill();
            let _ = child.wait();
            Err("Native Messaging host registration timed out".to_string())
        }
    }
}

#[derive(Default)]
struct NativeMessagingState {
    generation: AtomicU64,
    pending_pairing: Mutex<Option<PendingPairing>>,
    configuration: Mutex<Value>,
    latest_context: Mutex<Option<Value>>,
}

struct PendingPairing {
    nonce: String,
    expires_at: Instant,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct ExtensionConfiguration {
    origins: Vec<ConfiguredOrigin>,
    language: ExtensionLanguage,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ConfiguredOrigin {
    origin: String,
    mapping_id: String,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct CapturePayload {
    mapping_id: String,
    context: BrowserContext,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
enum ExtensionLanguage {
    En,
    Ko,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PairingStatus {
    paired: bool,
    pairing_nonce: Option<String>,
    expires_in_seconds: Option<u64>,
}

fn state() -> &'static Arc<NativeMessagingState> {
    STATE.get_or_init(|| Arc::new(NativeMessagingState::default()))
}

#[tauri::command]
pub fn native_messaging_begin_pairing() -> Result<PairingStatus, String> {
    clear_pairing_complete()?;
    let secret = generate_pairing_secret();
    write_pairing_secret(&secret)?;
    let nonce = random_nonce();
    *state()
        .pending_pairing
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(PendingPairing {
        nonce: nonce.clone(),
        expires_at: Instant::now() + PAIRING_NONCE_TTL,
    });
    state().generation.fetch_add(1, Ordering::SeqCst);
    info!(event = "native_messaging_pairing_started");
    Ok(PairingStatus {
        paired: false,
        pairing_nonce: Some(nonce),
        expires_in_seconds: Some(PAIRING_NONCE_TTL.as_secs()),
    })
}

#[tauri::command]
pub fn native_messaging_status() -> Result<PairingStatus, String> {
    Ok(PairingStatus {
        paired: read_pairing_secret()?.is_some() && pairing_is_complete()?,
        pairing_nonce: None,
        expires_in_seconds: None,
    })
}

#[tauri::command]
pub fn native_messaging_unpair() -> Result<PairingStatus, String> {
    invalidate_pairing()?;
    Ok(PairingStatus {
        paired: false,
        pairing_nonce: None,
        expires_in_seconds: None,
    })
}

#[tauri::command]
pub fn native_messaging_replace_configuration(configuration: Value) -> Result<(), String> {
    let encoded = serde_json::to_vec(&configuration).map_err(|_| "invalid-argument")?;
    if encoded.len() > devhud_native_messaging_host::MAX_JSON_BYTES {
        return Err("invalid-argument".to_string());
    }
    let configuration: ExtensionConfiguration =
        serde_json::from_value(configuration).map_err(|_| "invalid-argument")?;
    if configuration.origins.len() > 100
        || configuration.origins.iter().any(|mapping| {
            let Ok(id) = uuid::Uuid::parse_str(&mapping.mapping_id) else {
                return true;
            };
            let Ok(url) = url::Url::parse(&mapping.origin) else {
                return true;
            };
            id.get_version_num() != 7
                || !matches!(url.scheme(), "http" | "https")
                || !url.username().is_empty()
                || url.password().is_some()
                || url.query().is_some()
                || url.fragment().is_some()
                || url.path() != "/"
                || url.origin().ascii_serialization() != mapping.origin
        })
    {
        return Err("invalid-argument".to_string());
    }
    let configuration = serde_json::to_value(configuration).map_err(|_| "invalid-argument")?;
    *state()
        .configuration
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner) = configuration;
    Ok(())
}

#[tauri::command]
pub fn native_messaging_take_context() -> Option<Value> {
    state()
        .latest_context
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
        .take()
}

pub fn invalidate_pairing() -> Result<(), String> {
    delete_pairing_secret()?;
    *state()
        .pending_pairing
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner) = None;
    state()
        .latest_context
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
        .take();
    state().generation.fetch_add(1, Ordering::SeqCst);
    info!(event = "native_messaging_pairing_invalidated");
    Ok(())
}

fn pairing_nonce_is_valid(candidate: Option<&str>) -> bool {
    let pending = state()
        .pending_pairing
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let Some(current) = pending.as_ref() else {
        return candidate.is_none() && pairing_is_complete().unwrap_or(false);
    };
    current.expires_at >= Instant::now() && candidate == Some(current.nonce.as_str())
}

fn consume_pairing_nonce(candidate: &str) -> bool {
    let mut pending = state()
        .pending_pairing
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let accepted = pending
        .as_ref()
        .is_some_and(|current| current.expires_at >= Instant::now() && current.nonce == candidate);
    if accepted {
        *pending = None;
    }
    accepted
}

fn authorize_capture(payload: Value, configuration: &Value) -> Result<Value, &'static str> {
    let capture: CapturePayload =
        serde_json::from_value(payload).map_err(|_| "invalid-browser-context")?;
    validate_browser_context(&capture.context)?;
    let context_url =
        url::Url::parse(&capture.context.url).map_err(|_| "invalid-browser-context")?;
    let context_origin = context_url.origin().ascii_serialization();
    let configuration: ExtensionConfiguration =
        serde_json::from_value(configuration.clone()).map_err(|_| "capture-not-authorized")?;
    if !configuration
        .origins
        .iter()
        .any(|mapping| mapping.mapping_id == capture.mapping_id && mapping.origin == context_origin)
    {
        return Err("capture-not-authorized");
    }
    serde_json::to_value(capture).map_err(|_| "invalid-browser-context")
}

#[cfg(unix)]
pub fn start() -> Result<(), String> {
    use std::os::unix::{fs::FileTypeExt, net::UnixListener};
    let path = endpoint::socket_path().map_err(|error| error.to_string())?;
    endpoint::prepare_unix_parent(&path).map_err(|error| error.to_string())?;
    if path.exists() {
        let metadata = std::fs::symlink_metadata(&path).map_err(|error| error.to_string())?;
        if !metadata.file_type().is_socket() {
            return Err("refusing to replace a non-socket IPC path".to_string());
        }
        if std::os::unix::net::UnixStream::connect(&path).is_ok() {
            return Err("DevHUD IPC listener is already running".to_string());
        }
        std::fs::remove_file(&path).map_err(|error| error.to_string())?;
    }
    let listener = UnixListener::bind(&path).map_err(|error| error.to_string())?;
    endpoint::set_socket_permissions(&path).map_err(|error| error.to_string())?;
    std::thread::Builder::new()
        .name("devhud-native-messaging-ipc".into())
        .spawn(move || {
            for accepted in listener.incoming() {
                match accepted {
                    Ok(stream) => {
                        if !endpoint::peer_is_current_user(&stream).unwrap_or(false) {
                            warn!(event = "native_messaging_peer_rejected");
                            continue;
                        }
                        let _ = stream.set_read_timeout(Some(CONNECTION_TIMEOUT));
                        let _ = stream.set_write_timeout(Some(CONNECTION_TIMEOUT));
                        std::thread::spawn(move || {
                            if let Err(reason) = serve_connection(stream) {
                                warn!(event = "native_messaging_connection_closed", reason);
                            }
                        });
                    }
                    Err(reason) => error!(event = "native_messaging_accept_failed", %reason),
                }
            }
        })
        .map_err(|error| error.to_string())?;
    info!(event = "native_messaging_listener_started");
    Ok(())
}

#[cfg(windows)]
pub fn start() -> Result<(), String> {
    // Windows creation is kept in a dedicated implementation so no browser
    // integration enters mobile builds.
    start_windows_pipe()
}

#[cfg(windows)]
fn start_windows_pipe() -> Result<(), String> {
    let listener = endpoint::WindowsPipeListener::new().map_err(|error| error.to_string())?;
    std::thread::Builder::new()
        .name("devhud-native-messaging-ipc".into())
        .spawn(move || {
            loop {
                match listener.accept() {
                    Ok(stream) => {
                        std::thread::spawn(move || {
                            if let Err(reason) = serve_connection(stream) {
                                warn!(event = "native_messaging_connection_closed", reason);
                            }
                        });
                    }
                    Err(reason) => error!(event = "native_messaging_accept_failed", %reason),
                }
            }
        })
        .map_err(|error| error.to_string())?;
    info!(event = "native_messaging_listener_started");
    Ok(())
}

fn serve_connection(mut stream: impl io::Read + io::Write) -> Result<(), String> {
    let challenge = new_challenge(now_unix_millis());
    write_json(&mut stream, ByteOrder::LittleEndian, &challenge).map_err(|_| "write-failed")?;
    let response: AuthResponse =
        read_json(&mut stream, ByteOrder::LittleEndian).map_err(|_| "authentication-failed")?;
    let secret = read_pairing_secret()?.ok_or("not-paired")?;
    let mut authenticated = validate_version(response.version, response.schema_version).is_ok()
        && response.challenge_id == challenge.challenge_id
        && response.extension_id == configured_extension_id()
        && response.origin == expected_extension_origin()
        && challenge.deadline_unix_ms >= now_unix_millis()
        && pairing_nonce_is_valid(response.pairing_nonce.as_deref())
        && verify_handshake(&secret, &challenge, &response);
    if authenticated && let Some(pairing_nonce) = response.pairing_nonce.as_deref() {
        authenticated = consume_pairing_nonce(pairing_nonce) && mark_pairing_complete().is_ok();
    }
    if !authenticated {
        let denied = AuthResult {
            version: PROTOCOL_VERSION,
            schema_version: SCHEMA_VERSION,
            accepted: false,
            session_id: None,
            error: Some("authentication-failed".into()),
        };
        let _ = write_json(&mut stream, ByteOrder::LittleEndian, &denied);
        return Err("authentication-failed".to_string());
    }
    let session_id = random_nonce();
    let generation = state().generation.load(Ordering::SeqCst);
    write_json(
        &mut stream,
        ByteOrder::LittleEndian,
        &AuthResult {
            version: PROTOCOL_VERSION,
            schema_version: SCHEMA_VERSION,
            accepted: true,
            session_id: Some(session_id.clone()),
            error: None,
        },
    )
    .map_err(|_| "write-failed")?;
    let mut nonces = ReplayGuard::new(REPLAY_LIMIT);
    let mut requests = ReplayGuard::new(REPLAY_LIMIT);
    loop {
        let request: IpcRequest =
            read_json(&mut stream, ByteOrder::LittleEndian).map_err(|_| "read-failed")?;
        let now = now_unix_millis();
        let mut accepted = state().generation.load(Ordering::SeqCst) == generation
            && validate_version(request.version, request.schema_version).is_ok()
            && validate_deadline(request.issued_at_unix_ms, request.deadline_unix_ms, now).is_ok()
            && verify_request(&secret, &session_id, &request)
            && nonces.accept(request.nonce.clone())
            && requests.accept(request.request_id.clone());
        let mut payload = Value::Null;
        let mut error = None;
        if accepted {
            match request.message_type {
                NativeMessageType::Pair => payload = json!({ "paired": true }),
                NativeMessageType::Configure | NativeMessageType::Ping => {
                    payload = state()
                        .configuration
                        .lock()
                        .unwrap_or_else(std::sync::PoisonError::into_inner)
                        .clone();
                }
                NativeMessageType::Capture => {
                    let configuration = state()
                        .configuration
                        .lock()
                        .unwrap_or_else(std::sync::PoisonError::into_inner)
                        .clone();
                    match authorize_capture(request.payload, &configuration) {
                        Ok(context) => {
                            *state()
                                .latest_context
                                .lock()
                                .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(context);
                        }
                        Err(reason) => {
                            accepted = false;
                            error = Some(reason.to_string());
                        }
                    }
                }
            }
        } else {
            error = Some("request-rejected".to_string());
        }
        write_json(
            &mut stream,
            ByteOrder::LittleEndian,
            &IpcResponse {
                version: PROTOCOL_VERSION,
                schema_version: SCHEMA_VERSION,
                request_id: request.request_id,
                accepted,
                error,
                payload,
            },
        )
        .map_err(|_| "write-failed")?;
        if !accepted && state().generation.load(Ordering::SeqCst) != generation {
            return Err("pairing-invalidated".to_string());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pairing_nonce_is_one_time() {
        *state().pending_pairing.lock().unwrap() = Some(PendingPairing {
            nonce: "once".into(),
            expires_at: Instant::now() + Duration::from_secs(1),
        });
        assert!(pairing_nonce_is_valid(Some("once")));
        assert!(consume_pairing_nonce("once"));
        assert!(!consume_pairing_nonce("once"));
    }

    #[test]
    fn configuration_is_bounded() {
        assert!(
            native_messaging_replace_configuration(json!({
                "origins": [{
                    "origin": "https://example.com",
                    "mappingId": "01900000-0000-7000-8000-000000000001"
                }],
                "language": "en"
            }))
            .is_ok()
        );
        assert!(
            native_messaging_replace_configuration(Value::String(
                "x".repeat(devhud_native_messaging_host::MAX_JSON_BYTES + 1)
            ))
            .is_err()
        );
        assert!(
            native_messaging_replace_configuration(json!({
                "origins": [{
                    "origin": "https://unconfigured.example/path",
                    "mappingId": "01900000-0000-7000-8000-000000000001"
                }],
                "language": "en"
            }))
            .is_err()
        );
    }

    #[test]
    fn capture_requires_exact_configured_mapping_and_origin() {
        let configuration = json!({
            "origins": [{
                "origin": "https://example.com",
                "mappingId": "01900000-0000-7000-8000-000000000001"
            }],
            "language": "en"
        });
        let capture = json!({
            "mappingId": "01900000-0000-7000-8000-000000000001",
            "context": {
                "url": "https://example.com/%3Credacted%3E",
                "title": "Example",
                "viewport": { "width": 1280, "height": 720 },
                "userAgent": "test",
                "selectedBounds": { "x": 0, "y": 0, "width": 10, "height": 10 },
                "accessibility": { "role": "button" },
                "outerHtml": "<main role=\"button\">Safe</main>"
            }
        });
        let authorized = authorize_capture(capture.clone(), &configuration);
        assert!(authorized.is_ok(), "{authorized:?}");

        let mut wrong_origin = capture.clone();
        wrong_origin["context"]["url"] = json!("https://other.example/%3Credacted%3E");
        assert_eq!(
            authorize_capture(wrong_origin, &configuration),
            Err("capture-not-authorized")
        );

        let mut unknown_field = capture;
        unknown_field["cookies"] = json!("forbidden");
        assert_eq!(
            authorize_capture(unknown_field, &configuration),
            Err("invalid-browser-context")
        );
    }
}
