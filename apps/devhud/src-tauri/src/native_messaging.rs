use std::{
    collections::HashSet,
    io,
    sync::{
        Arc, Mutex, MutexGuard, OnceLock,
        atomic::{AtomicBool, AtomicU64, Ordering},
    },
    time::{Duration, Instant},
};

use devhud_native_messaging_host::{
    PROTOCOL_VERSION, SCHEMA_VERSION,
    auth::{
        ReplayGuard, auth_result_proof, new_challenge, now_unix_millis, random_nonce,
        verify_handshake, verify_request,
    },
    clear_pairing_complete, configured_extension_id, delete_pairing_secret, endpoint,
    expected_extension_origin,
    framing::{ByteOrder, read_json, write_json},
    generate_pairing_secret, mark_pairing_complete, pairing_is_complete,
    protocol::{
        AuthPurpose, AuthResponse, AuthResult, BrowserContext, IpcMessageType, IpcRequest,
        IpcResponse, NativeResponse, NativeResponseState, SESSION_INVALIDATED_ERROR,
        validate_browser_context, validate_deadline, validate_version,
    },
    read_pairing_secret, write_pairing_secret,
};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::{Value, json};
use tracing::{error, info, warn};
use uuid::Uuid;

use crate::capture::{CaptureService, DraftBrowserContext, DraftSummary};

const PAIRING_NONCE_TTL: Duration = Duration::from_secs(120);
const CONNECTION_TIMEOUT: Duration = Duration::from_secs(5);
const REPLAY_LIMIT: usize = 4_096;
#[cfg(any(unix, windows, test))]
const ACCEPT_RETRY_INITIAL_DELAY: Duration = Duration::from_millis(250);
#[cfg(any(unix, windows, test))]
const ACCEPT_RETRY_MAXIMUM_DELAY: Duration = Duration::from_secs(5);

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
    authentication_revoked: AtomicBool,
    generation: AtomicU64,
    pairing_lifecycle: Mutex<()>,
    pending_pairing: Mutex<Option<PendingPairing>>,
    configuration_scope: Mutex<Option<Uuid>>,
    configuration: Mutex<Value>,
    latest_context: Mutex<Option<Value>>,
}

pub(crate) struct PairingInvalidation<'a> {
    messaging_state: &'a NativeMessagingState,
    _lifecycle: MutexGuard<'a, ()>,
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
    mappings: Vec<ConfiguredMapping>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ConfiguredMapping {
    mapping_id: String,
    matcher: ConfiguredUrlMatcher,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ConfiguredUrlMatcher {
    scheme: String,
    host: Vec<String>,
    host_is_ip_literal: bool,
    port: String,
    path: Vec<String>,
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
    let _lifecycle = state()
        .pairing_lifecycle
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    invalidate_in_memory_pairing(state());
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
    state()
        .authentication_revoked
        .store(false, Ordering::SeqCst);
    info!(event = "native_messaging_pairing_started");
    Ok(PairingStatus {
        paired: false,
        pairing_nonce: Some(nonce),
        expires_in_seconds: Some(PAIRING_NONCE_TTL.as_secs()),
    })
}

#[tauri::command]
pub fn native_messaging_status() -> Result<PairingStatus, String> {
    let _lifecycle = state()
        .pairing_lifecycle
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let paired = if state().authentication_revoked.load(Ordering::SeqCst) {
        false
    } else {
        read_pairing_secret()?.is_some() && pairing_is_complete()?
    };
    Ok(PairingStatus {
        paired,
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
pub fn native_messaging_replace_configuration(
    configuration: Value,
    scope_id: String,
) -> Result<(), String> {
    replace_configuration(state(), configuration, &scope_id)
}

fn replace_configuration(
    messaging_state: &NativeMessagingState,
    configuration: Value,
    scope_id: &str,
) -> Result<(), String> {
    let parsed_scope_id = Uuid::parse_str(scope_id).map_err(|_| "invalid-argument")?;
    if parsed_scope_id.to_string() != scope_id {
        return Err("invalid-argument".to_string());
    }
    let validated = validate_configuration(configuration);
    let _lifecycle = messaging_state
        .pairing_lifecycle
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let mut current = messaging_state
        .configuration
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let mut current_scope = messaging_state
        .configuration_scope
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let scope_changed = current_scope.as_ref() != Some(&parsed_scope_id);
    if scope_changed {
        *current_scope = Some(parsed_scope_id);
    }
    match validated {
        Ok(configuration) => {
            if scope_changed || *current != configuration {
                messaging_state.generation.fetch_add(1, Ordering::SeqCst);
                let mut latest_context = messaging_state
                    .latest_context
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner);
                *current = configuration;
                latest_context.take();
            }
            Ok(())
        }
        Err(error) => {
            messaging_state.generation.fetch_add(1, Ordering::SeqCst);
            *current = Value::Null;
            messaging_state
                .latest_context
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .take();
            warn!(event = "native_messaging_configuration_rejected");
            Err(error)
        }
    }
}

fn validate_configuration(configuration: Value) -> Result<Value, String> {
    let encoded = serde_json::to_vec(&configuration).map_err(|_| "invalid-argument")?;
    if encoded.len() > devhud_native_messaging_host::MAX_JSON_BYTES {
        return Err("invalid-argument".to_string());
    }
    let configuration: ExtensionConfiguration =
        serde_json::from_value(configuration).map_err(|_| "invalid-argument")?;
    if !valid_extension_configuration(&configuration) {
        return Err("invalid-argument".to_string());
    }
    let configuration =
        serde_json::to_value(configuration).map_err(|_| "invalid-argument".to_string())?;
    if !configuration_response_envelopes_fit(&configuration) {
        return Err("invalid-argument".to_string());
    }
    Ok(configuration)
}

fn configuration_response_envelopes_fit(configuration: &Value) -> bool {
    const REQUEST_ID: &str = "01900000-0000-7000-8000-000000000000";
    let ipc = IpcResponse {
        version: PROTOCOL_VERSION,
        schema_version: SCHEMA_VERSION,
        request_id: REQUEST_ID.to_string(),
        accepted: true,
        error: None,
        payload: configuration.clone(),
    };
    let native = NativeResponse {
        version: PROTOCOL_VERSION,
        schema_version: SCHEMA_VERSION,
        request_id: REQUEST_ID.to_string(),
        ok: true,
        state: NativeResponseState::Accepted,
        payload: configuration.clone(),
    };
    [serde_json::to_vec(&ipc), serde_json::to_vec(&native)]
        .into_iter()
        .all(|encoded| {
            encoded
                .as_ref()
                .is_ok_and(|body| body.len() <= devhud_native_messaging_host::MAX_JSON_BYTES)
        })
}

fn valid_extension_configuration(configuration: &ExtensionConfiguration) -> bool {
    let mapping_count = configuration
        .origins
        .iter()
        .map(|origin| origin.mappings.len())
        .sum::<usize>();
    let unique_origins = configuration
        .origins
        .iter()
        .map(|origin| origin.origin.as_str())
        .collect::<HashSet<_>>();
    let unique_mapping_ids = configuration
        .origins
        .iter()
        .flat_map(|origin| {
            origin
                .mappings
                .iter()
                .map(|mapping| mapping.mapping_id.as_str())
        })
        .collect::<HashSet<_>>();
    configuration.origins.len() <= 100
        && mapping_count <= 100
        && unique_origins.len() == configuration.origins.len()
        && unique_mapping_ids.len() == mapping_count
        && configuration.origins.iter().all(|configured| {
            !configured.mappings.is_empty()
                && valid_configured_origin(&configured.origin)
                && configured.mappings.iter().all(|mapping| {
                    uuid::Uuid::parse_str(&mapping.mapping_id).is_ok_and(|id| {
                        id.get_version_num() == 7 && id.to_string() == mapping.mapping_id
                    }) && valid_configured_matcher(&mapping.matcher)
                        && configured_origin_matches(&configured.origin, &mapping.matcher)
                })
        })
}

fn configured_origin_matches(origin: &str, matcher: &ConfiguredUrlMatcher) -> bool {
    let Ok(url) = url::Url::parse(origin) else {
        return false;
    };
    if matcher.scheme != "*" && matcher.scheme != url.scheme() {
        return false;
    }
    let origin_port = url.port().map(|port| port.to_string()).unwrap_or_default();
    let matcher_port = match (url.scheme(), matcher.port.as_str()) {
        ("http", "80") | ("https", "443") => "",
        (_, port) => port,
    };
    if matcher_port != "*" && matcher_port != origin_port {
        return false;
    }
    let Some(host) = url.host() else {
        return false;
    };
    let (origin_host, origin_is_ip_literal) = match host {
        url::Host::Domain(domain) => {
            let mut labels = domain.split('.').collect::<Vec<_>>();
            if labels.len() > 1 && labels.last() == Some(&"") {
                labels.pop();
            }
            (labels.into_iter().map(str::to_string).collect(), false)
        }
        url::Host::Ipv4(address) => (
            address.to_string().split('.').map(str::to_string).collect(),
            true,
        ),
        url::Host::Ipv6(address) => (vec![format!("[{address}]")], true),
    };
    matcher.host.len() == origin_host.len()
        && matcher
            .host
            .iter()
            .zip(origin_host)
            .all(|(pattern, value)| {
                if pattern == "*" {
                    !matcher.host_is_ip_literal && !origin_is_ip_literal
                } else {
                    pattern.eq_ignore_ascii_case(&value)
                }
            })
}

fn valid_configured_origin(origin: &str) -> bool {
    let Ok(url) = url::Url::parse(origin) else {
        return false;
    };
    matches!(url.scheme(), "http" | "https")
        && url.username().is_empty()
        && url.password().is_none()
        && url.query().is_none()
        && url.fragment().is_none()
        && url.path() == "/"
        && url.origin().ascii_serialization() == origin
}

fn valid_configured_matcher(matcher: &ConfiguredUrlMatcher) -> bool {
    matches!(matcher.scheme.as_str(), "http" | "https" | "*")
        && !matcher.host.is_empty()
        && matcher.host.len() <= 128
        && matcher.host.iter().all(|part| {
            !part.is_empty()
                && part.len() <= 255
                && (part == "*"
                    || (!part.contains('*') && !part.contains('@') && !part.contains('\\')))
        })
        && (!matcher.host_is_ip_literal || valid_ip_literal_host(&matcher.host))
        && (matcher.port.is_empty()
            || matcher.port == "*"
            || matcher.port.parse::<u16>().is_ok_and(|port| port > 0))
        && matcher.path.len() <= 32
        && matcher
            .path
            .iter()
            .filter(|part| part.as_str() == "**")
            .count()
            <= 8
        && matcher
            .path
            .iter()
            .all(|part| part == "*" || part == "**" || !part.contains('*'))
}

fn valid_ip_literal_host(host: &[String]) -> bool {
    match host {
        [address] => address
            .strip_prefix('[')
            .and_then(|value| value.strip_suffix(']'))
            .is_some_and(|value| value.parse::<std::net::Ipv6Addr>().is_ok()),
        [first, second, third, fourth] => [first, second, third, fourth].into_iter().all(|part| {
            part.parse::<u8>()
                .is_ok_and(|octet| octet.to_string() == part.as_str())
        }),
        _ => false,
    }
}

#[cfg(any(unix, windows, test))]
fn updated_accept_retry_delay(current: Duration, accepted: bool) -> Duration {
    if accepted {
        ACCEPT_RETRY_INITIAL_DELAY
    } else {
        current.saturating_mul(2).min(ACCEPT_RETRY_MAXIMUM_DELAY)
    }
}

#[tauri::command]
pub fn native_messaging_take_context(
    draft_id: String,
    expected_revision: u64,
    capture: tauri::State<'_, Arc<CaptureService>>,
) -> Result<Option<DraftSummary>, String> {
    let draft_id = Uuid::parse_str(&draft_id).map_err(|_| "invalid-argument".to_string())?;
    let result = take_latest_context_with(state(), |value| {
        let captured: CapturePayload =
            serde_json::from_value(value).map_err(|_| "invalid-argument".to_string())?;
        capture
            .with_draft_store(|store| {
                store.attach_browser_context(
                    draft_id,
                    expected_revision,
                    DraftBrowserContext {
                        mapping_id: captured.mapping_id,
                        context: captured.context,
                    },
                )
            })
            .map_err(|error| error.code().to_string())
    });
    match &result {
        Ok(Some(_)) => info!(event = "native_messaging_context_attached", %draft_id),
        Ok(None) => {}
        Err(error_code) => warn!(
            event = "native_messaging_context_attachment_failed",
            %draft_id,
            %error_code,
        ),
    }
    result
}

fn take_latest_context_with<Output>(
    messaging_state: &NativeMessagingState,
    attach: impl FnOnce(Value) -> Result<Output, String>,
) -> Result<Option<Output>, String> {
    let _lifecycle = messaging_state
        .pairing_lifecycle
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let context = messaging_state
        .latest_context
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
        .take();
    context.map(attach).transpose()
}

pub fn invalidate_pairing() -> Result<(), String> {
    begin_pairing_invalidation().commit()
}

pub(crate) fn begin_pairing_invalidation() -> PairingInvalidation<'static> {
    begin_pairing_invalidation_with(state())
}

fn begin_pairing_invalidation_with(
    messaging_state: &NativeMessagingState,
) -> PairingInvalidation<'_> {
    let lifecycle = messaging_state
        .pairing_lifecycle
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    invalidate_in_memory_pairing(messaging_state);
    PairingInvalidation {
        messaging_state,
        _lifecycle: lifecycle,
    }
}

impl PairingInvalidation<'_> {
    pub(crate) fn commit(self) -> Result<(), String> {
        match self.commit_with(delete_pairing_secret) {
            Ok(()) => {
                info!(event = "native_messaging_pairing_invalidated");
                Ok(())
            }
            Err(error) => {
                warn!(event = "native_messaging_pairing_storage_cleanup_failed");
                Err(error)
            }
        }
    }

    fn commit_with(self, delete: impl FnOnce() -> Result<(), String>) -> Result<(), String> {
        delete()?;
        self.messaging_state
            .authentication_revoked
            .store(false, Ordering::SeqCst);
        Ok(())
    }
}

fn commit_latest_context_if_current(
    messaging_state: &NativeMessagingState,
    generation: u64,
    context: Value,
) -> bool {
    let _lifecycle = messaging_state
        .pairing_lifecycle
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    if !session_generation_is_current(messaging_state, generation) {
        return false;
    }
    *messaging_state
        .latest_context
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner) = Some(context);
    true
}

fn invalidate_in_memory_pairing(messaging_state: &NativeMessagingState) {
    messaging_state
        .authentication_revoked
        .store(true, Ordering::SeqCst);
    *messaging_state
        .pending_pairing
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner) = None;
    messaging_state
        .latest_context
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
        .take();
    messaging_state.generation.fetch_add(1, Ordering::SeqCst);
}

fn pairing_nonce_is_valid_with(
    messaging_state: &NativeMessagingState,
    candidate: Option<&str>,
    pairing_is_complete: impl FnOnce() -> bool,
) -> bool {
    if messaging_state
        .authentication_revoked
        .load(Ordering::SeqCst)
    {
        return false;
    }
    let pending = messaging_state
        .pending_pairing
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let Some(current) = pending.as_ref() else {
        return candidate.is_none() && pairing_is_complete();
    };
    current.expires_at >= Instant::now() && candidate == Some(current.nonce.as_str())
}

fn authentication_purpose_is_valid_with(
    messaging_state: &NativeMessagingState,
    purpose: AuthPurpose,
    pairing_nonce: Option<&str>,
    pairing_is_complete: impl FnOnce() -> bool,
) -> bool {
    match purpose {
        AuthPurpose::BrowserSession => {
            pairing_nonce_is_valid_with(messaging_state, pairing_nonce, pairing_is_complete)
        }
        AuthPurpose::PairingRevocation => {
            pairing_nonce.is_none()
                && !messaging_state
                    .authentication_revoked
                    .load(Ordering::SeqCst)
        }
    }
}

fn authentication_purpose_allows_message(
    purpose: AuthPurpose,
    message_type: IpcMessageType,
) -> bool {
    match purpose {
        AuthPurpose::BrowserSession => message_type != IpcMessageType::RevokePairing,
        AuthPurpose::PairingRevocation => message_type == IpcMessageType::RevokePairing,
    }
}

fn complete_pairing_with(
    messaging_state: &NativeMessagingState,
    generation: u64,
    candidate: &str,
    mark_complete: impl FnOnce() -> Result<(), String>,
) -> bool {
    let _lifecycle = messaging_state
        .pairing_lifecycle
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    if !session_generation_is_current(messaging_state, generation) {
        return false;
    }
    let mut pending = messaging_state
        .pending_pairing
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    if !pending
        .as_ref()
        .is_some_and(|current| current.expires_at >= Instant::now() && current.nonce == candidate)
        || mark_complete().is_err()
    {
        return false;
    }
    *pending = None;
    true
}

fn session_generation_is_current(messaging_state: &NativeMessagingState, generation: u64) -> bool {
    !messaging_state
        .authentication_revoked
        .load(Ordering::SeqCst)
        && messaging_state.generation.load(Ordering::SeqCst) == generation
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
    if !configuration.origins.iter().any(|origin| {
        origin.origin == context_origin
            && origin
                .mappings
                .iter()
                .any(|mapping| mapping.mapping_id == capture.mapping_id)
    }) {
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
            let mut retry_delay = ACCEPT_RETRY_INITIAL_DELAY;
            loop {
                match listener.accept() {
                    Ok((stream, _)) => {
                        retry_delay = updated_accept_retry_delay(retry_delay, true);
                        if !endpoint::peer_is_current_user(&stream).unwrap_or(false) {
                            warn!(event = "native_messaging_peer_rejected");
                            continue;
                        }
                        let stream = endpoint::IpcClientStream::from_unix_stream(stream);
                        std::thread::spawn(move || {
                            if let Err(reason) = serve_connection(stream) {
                                warn!(event = "native_messaging_connection_closed", reason);
                            }
                        });
                    }
                    Err(reason) => {
                        error!(event = "native_messaging_accept_failed", %reason);
                        std::thread::sleep(retry_delay);
                        retry_delay = updated_accept_retry_delay(retry_delay, false);
                    }
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
            let mut retry_delay = ACCEPT_RETRY_INITIAL_DELAY;
            loop {
                match listener.accept() {
                    Ok(stream) => {
                        retry_delay = updated_accept_retry_delay(retry_delay, true);
                        std::thread::spawn(move || {
                            if let Err(reason) = serve_connection(stream) {
                                warn!(event = "native_messaging_connection_closed", reason);
                            }
                        });
                    }
                    Err(reason) => {
                        error!(event = "native_messaging_accept_failed", %reason);
                        std::thread::sleep(retry_delay);
                        retry_delay = updated_accept_retry_delay(retry_delay, false);
                    }
                }
            }
        })
        .map_err(|error| error.to_string())?;
    info!(event = "native_messaging_listener_started");
    Ok(())
}

trait ConnectionStream: io::Read + io::Write {
    fn reset_io_deadline(&mut self, timeout: Duration) -> io::Result<()>;
}

#[cfg(unix)]
impl ConnectionStream for endpoint::IpcClientStream {
    fn reset_io_deadline(&mut self, timeout: Duration) -> io::Result<()> {
        self.set_io_deadline(timeout);
        Ok(())
    }
}

#[cfg(windows)]
impl ConnectionStream for endpoint::WindowsPipeStream {
    fn reset_io_deadline(&mut self, timeout: Duration) -> io::Result<()> {
        self.set_io_deadline(timeout);
        Ok(())
    }
}

fn read_connection_json<T: DeserializeOwned>(stream: &mut impl ConnectionStream) -> io::Result<T> {
    read_connection_json_with_timeout(stream, CONNECTION_TIMEOUT)
}

fn read_connection_json_with_timeout<T: DeserializeOwned>(
    stream: &mut impl ConnectionStream,
    timeout: Duration,
) -> io::Result<T> {
    stream.reset_io_deadline(timeout)?;
    read_json(stream, ByteOrder::LittleEndian)
}

fn write_connection_json<T: Serialize>(
    stream: &mut impl ConnectionStream,
    value: &T,
) -> io::Result<()> {
    stream.reset_io_deadline(CONNECTION_TIMEOUT)?;
    write_json(stream, ByteOrder::LittleEndian, value)
}

fn request_rejection_reason(generation_is_current: bool, reason: &str) -> &str {
    if generation_is_current {
        reason
    } else {
        SESSION_INVALIDATED_ERROR
    }
}

fn serve_connection(mut stream: impl ConnectionStream) -> Result<(), String> {
    let challenge = new_challenge(now_unix_millis());
    write_connection_json(&mut stream, &challenge).map_err(|_| "write-failed")?;
    let response: AuthResponse =
        read_connection_json(&mut stream).map_err(|_| "authentication-failed")?;
    let generation = state().generation.load(Ordering::SeqCst);
    let secret = read_pairing_secret()?.ok_or("not-paired")?;
    let mut authenticated = validate_version(response.version, response.schema_version).is_ok()
        && response.challenge_id == challenge.challenge_id
        && response.extension_id == configured_extension_id()
        && response.origin == expected_extension_origin()
        && challenge.deadline_unix_ms >= now_unix_millis()
        && authentication_purpose_is_valid_with(
            state(),
            response.purpose,
            response.pairing_nonce.as_deref(),
            || pairing_is_complete().unwrap_or(false),
        )
        && verify_handshake(&secret, &challenge, &response);
    if authenticated
        && response.purpose == AuthPurpose::BrowserSession
        && let Some(pairing_nonce) = response.pairing_nonce.as_deref()
    {
        authenticated =
            complete_pairing_with(state(), generation, pairing_nonce, mark_pairing_complete);
    }
    authenticated = authenticated && session_generation_is_current(state(), generation);
    if !authenticated {
        let denied = AuthResult {
            version: PROTOCOL_VERSION,
            schema_version: SCHEMA_VERSION,
            accepted: false,
            session_id: None,
            proof: None,
            error: Some("authentication-failed".into()),
        };
        let _ = write_connection_json(&mut stream, &denied);
        return Err("authentication-failed".to_string());
    }
    let session_id = random_nonce();
    write_connection_json(
        &mut stream,
        &AuthResult {
            version: PROTOCOL_VERSION,
            schema_version: SCHEMA_VERSION,
            accepted: true,
            session_id: Some(session_id.clone()),
            proof: Some(auth_result_proof(&secret, &challenge, &session_id)),
            error: None,
        },
    )
    .map_err(|_| "write-failed")?;
    let mut nonces = ReplayGuard::new(REPLAY_LIMIT);
    let mut requests = ReplayGuard::new(REPLAY_LIMIT);
    loop {
        let request: IpcRequest = read_connection_json(&mut stream).map_err(|_| "read-failed")?;
        let now = now_unix_millis();
        let mut accepted = session_generation_is_current(state(), generation)
            && validate_version(request.version, request.schema_version).is_ok()
            && validate_deadline(request.issued_at_unix_ms, request.deadline_unix_ms, now).is_ok()
            && authentication_purpose_allows_message(response.purpose, request.message_type)
            && verify_request(&secret, &session_id, &request)
            && nonces.accept(request.nonce.clone())
            && requests.accept(request.request_id.clone());
        let mut payload = Value::Null;
        let mut error = None;
        let mut close_after_response = false;
        if accepted {
            match request.message_type {
                IpcMessageType::Pair => payload = json!({ "paired": true }),
                IpcMessageType::Configure | IpcMessageType::Ping => {
                    payload = state()
                        .configuration
                        .lock()
                        .unwrap_or_else(std::sync::PoisonError::into_inner)
                        .clone();
                }
                IpcMessageType::Capture => {
                    let configuration = {
                        state()
                            .configuration
                            .lock()
                            .unwrap_or_else(std::sync::PoisonError::into_inner)
                            .clone()
                    };
                    match authorize_capture(request.payload, &configuration) {
                        Ok(context) => {
                            if !commit_latest_context_if_current(state(), generation, context) {
                                accepted = false;
                                error = Some("request-rejected".to_string());
                            }
                        }
                        Err(reason) => {
                            accepted = false;
                            error = Some(reason.to_string());
                        }
                    }
                }
                IpcMessageType::RevokePairing => {
                    close_after_response = true;
                    match invalidate_pairing() {
                        Ok(()) => payload = json!({ "revoked": true }),
                        Err(_) => {
                            accepted = false;
                            error = Some("storage-failure".to_string());
                        }
                    }
                }
            }
        } else {
            error = Some("request-rejected".to_string());
        }
        if !accepted && !close_after_response {
            error = Some(
                request_rejection_reason(
                    session_generation_is_current(state(), generation),
                    error.as_deref().unwrap_or("request-rejected"),
                )
                .to_string(),
            );
        }
        write_connection_json(
            &mut stream,
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
        if close_after_response {
            return if accepted {
                Ok(())
            } else {
                Err("pairing-invalidation-failed".to_string())
            };
        }
        if !accepted && !session_generation_is_current(state(), generation) {
            return Err("pairing-invalidated".to_string());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stale_session_rejections_have_a_retryable_reason() {
        assert_eq!(
            request_rejection_reason(false, "request-rejected"),
            SESSION_INVALIDATED_ERROR
        );
        assert_eq!(
            request_rejection_reason(false, "invalid-browser-context"),
            SESSION_INVALIDATED_ERROR
        );
        assert_eq!(
            request_rejection_reason(true, "invalid-browser-context"),
            "invalid-browser-context"
        );
    }

    fn configured_origin(origin: &str) -> Value {
        json!({
            "origin": origin,
            "mappings": [{
                "mappingId": "01900000-0000-7000-8000-000000000001",
                "matcher": {
                    "scheme": "https",
                    "host": ["example", "com"],
                    "hostIsIpLiteral": false,
                    "port": "",
                    "path": ["**"]
                }
            }]
        })
    }

    fn response_oversized_configuration() -> Value {
        let mut configuration = json!({
            "origins": [configured_origin("https://example.com")],
            "language": "en"
        });
        let initial_length = serde_json::to_vec(&configuration).unwrap().len();
        let filler_length = devhud_native_messaging_host::MAX_JSON_BYTES - initial_length + 1;
        configuration["origins"][0]["mappings"][0]["matcher"]["path"][0] =
            Value::String("x".repeat(filler_length));
        assert_eq!(
            serde_json::to_vec(&configuration).unwrap().len(),
            devhud_native_messaging_host::MAX_JSON_BYTES - 1
        );
        configuration
    }

    #[test]
    fn latest_context_is_consumed_once_by_the_draft_attachment() {
        let messaging_state = NativeMessagingState::default();
        *messaging_state.latest_context.lock().unwrap() = Some(json!({ "captured": true }));

        let attached = take_latest_context_with(&messaging_state, |value| {
            assert_eq!(value, json!({ "captured": true }));
            Ok("attached")
        })
        .unwrap();

        assert_eq!(attached, Some("attached"));
        assert!(messaging_state.latest_context.lock().unwrap().is_none());
        assert_eq!(
            take_latest_context_with(&messaging_state, |_| Ok("unexpected")).unwrap(),
            None
        );
    }

    #[test]
    fn failed_draft_attachment_drops_context_instead_of_reusing_it() {
        let messaging_state = NativeMessagingState::default();
        *messaging_state.latest_context.lock().unwrap() = Some(json!({ "captured": true }));

        assert_eq!(
            take_latest_context_with::<()>(&messaging_state, |_| {
                Err("storage-failure".to_string())
            }),
            Err("storage-failure".to_string())
        );
        assert!(messaging_state.latest_context.lock().unwrap().is_none());
    }

    #[test]
    fn pairing_nonce_is_consumed_only_after_completion_persists() {
        let messaging_state = NativeMessagingState::default();
        let generation = messaging_state.generation.load(Ordering::SeqCst);
        *messaging_state.pending_pairing.lock().unwrap() = Some(PendingPairing {
            nonce: "once".into(),
            expires_at: Instant::now() + Duration::from_secs(1),
        });
        let mut completion_attempts = 0;

        assert!(!complete_pairing_with(
            &messaging_state,
            generation,
            "once",
            || {
                completion_attempts += 1;
                Err("storage-failure".to_string())
            }
        ));
        assert!(pairing_nonce_is_valid_with(
            &messaging_state,
            Some("once"),
            || false
        ));
        assert!(complete_pairing_with(
            &messaging_state,
            generation,
            "once",
            || {
                completion_attempts += 1;
                Ok(())
            }
        ));
        assert!(!complete_pairing_with(
            &messaging_state,
            generation,
            "once",
            || {
                completion_attempts += 1;
                Ok(())
            }
        ));
        assert_eq!(completion_attempts, 2);
    }

    #[test]
    fn pending_pairing_allows_only_secret_authenticated_revocation_without_a_nonce() {
        let messaging_state = NativeMessagingState::default();
        *messaging_state.pending_pairing.lock().unwrap() = Some(PendingPairing {
            nonce: "pending".into(),
            expires_at: Instant::now() + Duration::from_secs(1),
        });

        assert!(!authentication_purpose_is_valid_with(
            &messaging_state,
            AuthPurpose::BrowserSession,
            None,
            || false,
        ));
        assert!(authentication_purpose_is_valid_with(
            &messaging_state,
            AuthPurpose::PairingRevocation,
            None,
            || false,
        ));
        assert!(!authentication_purpose_is_valid_with(
            &messaging_state,
            AuthPurpose::PairingRevocation,
            Some("pending"),
            || false,
        ));
        assert!(authentication_purpose_allows_message(
            AuthPurpose::PairingRevocation,
            IpcMessageType::RevokePairing,
        ));
        assert!(!authentication_purpose_allows_message(
            AuthPurpose::PairingRevocation,
            IpcMessageType::Configure,
        ));
        assert!(!authentication_purpose_allows_message(
            AuthPurpose::BrowserSession,
            IpcMessageType::RevokePairing,
        ));
    }

    #[test]
    fn configuration_is_bounded() {
        assert!(
            native_messaging_replace_configuration(
                json!({
                    "origins": [configured_origin("https://example.com")],
                    "language": "en"
                }),
                Uuid::now_v7().to_string()
            )
            .is_ok()
        );
        assert!(
            native_messaging_replace_configuration(
                Value::String("x".repeat(devhud_native_messaging_host::MAX_JSON_BYTES + 1)),
                Uuid::now_v7().to_string()
            )
            .is_err()
        );
        assert!(
            native_messaging_replace_configuration(
                response_oversized_configuration(),
                Uuid::now_v7().to_string()
            )
            .is_err()
        );
        assert!(
            native_messaging_replace_configuration(
                json!({
                    "origins": [configured_origin("https://unconfigured.example/path")],
                    "language": "en"
                }),
                Uuid::now_v7().to_string()
            )
            .is_err()
        );
        assert!(
            native_messaging_replace_configuration(
                json!({
                    "origins": [configured_origin("https://other.example")],
                    "language": "en"
                }),
                Uuid::now_v7().to_string()
            )
            .is_err()
        );
    }

    #[test]
    fn configuration_mapping_ids_must_be_canonical_uuid_v7() {
        let mut configuration = json!({
            "origins": [configured_origin("https://example.com")],
            "language": "en"
        });
        let uppercase = "018F47A2-7B3C-7DEF-8ABC-1234567890AB";
        assert!(Uuid::parse_str(uppercase).is_ok_and(|id| id.get_version_num() == 7));
        configuration["origins"][0]["mappings"][0]["mappingId"] =
            Value::String(uppercase.to_string());

        assert!(validate_configuration(configuration).is_err());
    }

    #[test]
    fn configured_origin_must_match_the_mapping_authority() {
        let wildcard = ConfiguredUrlMatcher {
            scheme: "*".into(),
            host: vec!["*".into(), "example".into()],
            host_is_ip_literal: false,
            port: "*".into(),
            path: vec!["private".into(), "**".into()],
        };
        assert!(configured_origin_matches(
            "https://app.example:8443",
            &wildcard
        ));
        assert!(!configured_origin_matches(
            "https://app.other:8443",
            &wildcard
        ));
        let fixed_port = ConfiguredUrlMatcher {
            scheme: "https".into(),
            host: vec!["app".into(), "example".into()],
            host_is_ip_literal: false,
            port: "8443".into(),
            path: vec![],
        };
        assert!(!configured_origin_matches(
            "https://app.example:9443",
            &fixed_port
        ));
    }

    #[test]
    fn canonical_ip_literal_matchers_are_valid() {
        let ipv4 = ConfiguredUrlMatcher {
            scheme: "http".into(),
            host: ["127", "0", "0", "1"].map(str::to_string).into(),
            host_is_ip_literal: true,
            port: "".into(),
            path: vec!["**".into()],
        };
        assert!(valid_configured_matcher(&ipv4));
        assert!(configured_origin_matches("http://127.0.0.1", &ipv4));

        let ipv6 = ConfiguredUrlMatcher {
            scheme: "http".into(),
            host: vec!["[::1]".into()],
            host_is_ip_literal: true,
            port: "".into(),
            path: vec!["**".into()],
        };
        assert!(valid_configured_matcher(&ipv6));
        assert!(configured_origin_matches("http://[::1]", &ipv6));
    }

    #[test]
    fn accept_retry_delay_is_capped_and_resets_after_success() {
        assert_eq!(
            updated_accept_retry_delay(ACCEPT_RETRY_INITIAL_DELAY, false),
            Duration::from_millis(500)
        );
        assert_eq!(
            updated_accept_retry_delay(ACCEPT_RETRY_MAXIMUM_DELAY, false),
            ACCEPT_RETRY_MAXIMUM_DELAY
        );
        assert_eq!(
            updated_accept_retry_delay(ACCEPT_RETRY_MAXIMUM_DELAY, true),
            ACCEPT_RETRY_INITIAL_DELAY
        );
    }

    #[test]
    fn rejected_configuration_clears_prior_authorization_and_context() {
        let messaging_state = NativeMessagingState::default();
        let scope_id = Uuid::now_v7().to_string();
        assert!(
            replace_configuration(
                &messaging_state,
                json!({
                    "origins": [configured_origin("https://example.com")],
                    "language": "en"
                }),
                &scope_id,
            )
            .is_ok()
        );
        *messaging_state.latest_context.lock().unwrap() = Some(json!({ "prior": true }));
        let generation = messaging_state.generation.load(Ordering::SeqCst);

        assert!(
            replace_configuration(
                &messaging_state,
                response_oversized_configuration(),
                &scope_id,
            )
            .is_err()
        );
        assert!(messaging_state.configuration.lock().unwrap().is_null());
        assert!(messaging_state.latest_context.lock().unwrap().is_none());
        assert_eq!(
            messaging_state.generation.load(Ordering::SeqCst),
            generation + 1
        );
        assert!(!commit_latest_context_if_current(
            &messaging_state,
            generation,
            json!({ "stale": true }),
        ));
    }

    #[test]
    fn changed_configuration_clears_prior_context() {
        let messaging_state = NativeMessagingState::default();
        let initial = json!({
            "origins": [configured_origin("https://example.com")],
            "language": "en"
        });
        let scope_id = Uuid::now_v7().to_string();
        assert!(replace_configuration(&messaging_state, initial, &scope_id).is_ok());
        *messaging_state.latest_context.lock().unwrap() = Some(json!({ "prior": true }));
        let replacement = json!({
            "origins": [{
                "origin": "https://example.com",
                "mappings": [{
                    "mappingId": "01900000-0000-7000-8000-000000000001",
                    "matcher": {
                        "scheme": "https",
                        "host": ["example", "com"],
                        "hostIsIpLiteral": false,
                        "port": "",
                        "path": ["changed", "**"]
                    }
                }]
            }],
            "language": "en"
        });
        let generation = messaging_state.generation.load(Ordering::SeqCst);

        assert!(replace_configuration(&messaging_state, replacement.clone(), &scope_id).is_ok());
        assert!(messaging_state.latest_context.lock().unwrap().is_none());
        assert_eq!(
            messaging_state.generation.load(Ordering::SeqCst),
            generation + 1
        );
        assert!(!commit_latest_context_if_current(
            &messaging_state,
            generation,
            json!({ "stale": true }),
        ));

        *messaging_state.latest_context.lock().unwrap() = Some(json!({ "current": true }));
        let generation = messaging_state.generation.load(Ordering::SeqCst);
        assert!(replace_configuration(&messaging_state, replacement, &scope_id).is_ok());
        assert_eq!(
            *messaging_state.latest_context.lock().unwrap(),
            Some(json!({ "current": true }))
        );
        assert_eq!(
            messaging_state.generation.load(Ordering::SeqCst),
            generation
        );
    }

    #[test]
    fn changed_identity_scope_clears_context_for_identical_configuration() {
        let messaging_state = NativeMessagingState::default();
        let configuration = json!({
            "origins": [configured_origin("https://example.com")],
            "language": "en"
        });
        let first_scope = Uuid::now_v7().to_string();
        let second_scope = Uuid::now_v7().to_string();
        assert!(
            replace_configuration(&messaging_state, configuration.clone(), &first_scope).is_ok()
        );
        *messaging_state.latest_context.lock().unwrap() = Some(json!({ "prior": true }));
        let first_generation = messaging_state.generation.load(Ordering::SeqCst);

        assert!(replace_configuration(&messaging_state, configuration, &second_scope).is_ok());

        assert!(messaging_state.latest_context.lock().unwrap().is_none());
        assert_eq!(
            messaging_state.generation.load(Ordering::SeqCst),
            first_generation + 1
        );
    }

    #[test]
    fn in_memory_pairing_is_invalidated_independently_of_storage_cleanup() {
        let messaging_state = NativeMessagingState::default();
        *messaging_state.pending_pairing.lock().unwrap() = Some(PendingPairing {
            nonce: "pending".into(),
            expires_at: Instant::now() + Duration::from_secs(1),
        });
        *messaging_state.latest_context.lock().unwrap() = Some(json!({ "prior": true }));

        invalidate_in_memory_pairing(&messaging_state);

        assert!(messaging_state.pending_pairing.lock().unwrap().is_none());
        assert!(messaging_state.latest_context.lock().unwrap().is_none());
        assert!(
            messaging_state
                .authentication_revoked
                .load(Ordering::SeqCst)
        );
        assert_eq!(messaging_state.generation.load(Ordering::SeqCst), 1);
    }

    #[test]
    fn pairing_credentials_are_not_deleted_until_invalidation_is_committed() {
        let messaging_state = NativeMessagingState::default();
        let deletion_called = AtomicBool::new(false);

        let invalidation = begin_pairing_invalidation_with(&messaging_state);

        assert!(
            messaging_state
                .authentication_revoked
                .load(Ordering::SeqCst)
        );
        assert!(!deletion_called.load(Ordering::SeqCst));
        invalidation
            .commit_with(|| {
                deletion_called.store(true, Ordering::SeqCst);
                Ok(())
            })
            .unwrap();
        assert!(deletion_called.load(Ordering::SeqCst));
    }

    #[test]
    fn revoked_generation_cannot_commit_captured_context() {
        let messaging_state = NativeMessagingState::default();
        let generation = messaging_state.generation.load(Ordering::SeqCst);
        assert!(commit_latest_context_if_current(
            &messaging_state,
            generation,
            json!({ "current": true }),
        ));

        drop(begin_pairing_invalidation_with(&messaging_state));

        assert!(!commit_latest_context_if_current(
            &messaging_state,
            generation,
            json!({ "stale": true }),
        ));
        assert!(messaging_state.latest_context.lock().unwrap().is_none());
    }

    #[test]
    fn failed_pairing_deletion_blocks_reauthentication() {
        let messaging_state = NativeMessagingState::default();
        let result = begin_pairing_invalidation_with(&messaging_state)
            .commit_with(|| Err("storage-failure".to_string()));

        assert_eq!(result, Err("storage-failure".to_string()));
        assert!(
            messaging_state
                .authentication_revoked
                .load(Ordering::SeqCst)
        );
        assert!(!pairing_nonce_is_valid_with(&messaging_state, None, || {
            true
        }));
    }

    #[test]
    fn revoked_or_replaced_generation_cannot_authenticate_a_session() {
        let messaging_state = NativeMessagingState::default();
        let generation = messaging_state.generation.load(Ordering::SeqCst);
        assert!(session_generation_is_current(&messaging_state, generation));

        messaging_state
            .authentication_revoked
            .store(true, Ordering::SeqCst);
        assert!(!session_generation_is_current(&messaging_state, generation));

        messaging_state
            .authentication_revoked
            .store(false, Ordering::SeqCst);
        messaging_state.generation.fetch_add(1, Ordering::SeqCst);
        assert!(!session_generation_is_current(&messaging_state, generation));
    }

    #[test]
    fn replaced_generation_cannot_write_pairing_completion() {
        let messaging_state = NativeMessagingState::default();
        let generation = messaging_state.generation.load(Ordering::SeqCst);
        *messaging_state.pending_pairing.lock().unwrap() = Some(PendingPairing {
            nonce: "old".into(),
            expires_at: Instant::now() + Duration::from_secs(1),
        });
        invalidate_in_memory_pairing(&messaging_state);
        *messaging_state.pending_pairing.lock().unwrap() = Some(PendingPairing {
            nonce: "replacement".into(),
            expires_at: Instant::now() + Duration::from_secs(1),
        });
        messaging_state
            .authentication_revoked
            .store(false, Ordering::SeqCst);
        let mut completion_written = false;

        assert!(!complete_pairing_with(
            &messaging_state,
            generation,
            "old",
            || {
                completion_written = true;
                Ok(())
            }
        ));
        assert!(!completion_written);
        assert_eq!(
            messaging_state
                .pending_pairing
                .lock()
                .unwrap()
                .as_ref()
                .map(|pending| pending.nonce.as_str()),
            Some("replacement")
        );
    }

    #[cfg(unix)]
    #[test]
    fn partial_unix_app_frame_is_bounded_by_one_absolute_deadline() {
        use std::{io::Write, os::unix::net::UnixStream, thread};

        let (mut peer, stream) = UnixStream::pair().unwrap();
        let writer = thread::spawn(move || {
            for byte in [2, 0, 0, 0, b'{', b'}'] {
                if peer.write_all(&[byte]).is_err() {
                    break;
                }
                thread::sleep(Duration::from_millis(20));
            }
        });
        let mut stream = endpoint::IpcClientStream::from_unix_stream(stream);
        let error =
            read_connection_json_with_timeout::<Value>(&mut stream, Duration::from_millis(50))
                .unwrap_err();

        assert!(matches!(
            error.kind(),
            io::ErrorKind::TimedOut | io::ErrorKind::WouldBlock
        ));
        drop(stream);
        writer.join().unwrap();
    }

    #[test]
    fn capture_requires_exact_configured_mapping_and_origin() {
        let configuration = json!({
            "origins": [configured_origin("https://example.com")],
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

        let mut wrong_port = capture.clone();
        wrong_port["context"]["url"] = json!("https://example.com:8443/%3Credacted%3E");
        assert_eq!(
            authorize_capture(wrong_port, &configuration),
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
