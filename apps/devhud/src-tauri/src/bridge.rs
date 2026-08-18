#[cfg(desktop)]
use std::io::Write;
use std::sync::{
    Arc, Condvar, Mutex,
    atomic::{AtomicBool, Ordering},
};
#[cfg(desktop)]
use std::time::Duration;

use serde_json::{Value, json};

#[cfg(desktop)]
use crate::shortcuts::{NativeKeyEvent, ShortcutAction};
use crate::shortcuts::{
    PlatformShortcutBackend, ShortcutBindings, ShortcutFailure, ShortcutService,
};

const PROFILE_ID_LIMIT: usize = 128;
const SECRET_LIMIT: usize = 64 * 1024;
const DIAGNOSTICS_EXPORT_LIMIT: usize = 1024 * 1024;
const TAURI_REVISION: &str = "4af26a3f7f8b692d62cca549bbacd93f5ce90b41";
#[cfg(not(any(target_os = "android", target_os = "ios")))]
const CEF_REVISION: &str = "150.0.10+g8042e43+chromium-150.0.7871.101";

const DEFAULT_API_ORIGIN: &str = "https://devhud.api.delino.io";

#[derive(Clone)]
pub struct NativeBridgeState {
    pending_auth_callback: Arc<Mutex<Option<String>>>,
    session_origins: Arc<Mutex<SessionOrigins>>,
    shortcuts: Arc<Mutex<ShortcutService<PlatformShortcutBackend>>>,
    shortcuts_ready: Arc<AtomicBool>,
    shortcut_listener_failed: Arc<AtomicBool>,
    shortcut_listener_retry: Arc<(Mutex<u64>, Condvar)>,
    diagnostics_export_generation: Arc<Mutex<u64>>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct SessionOrigins {
    api_origin: String,
    logto_issuer: Option<url::Url>,
}

impl Default for NativeBridgeState {
    fn default() -> Self {
        let shortcut_listener_failed = Arc::new(AtomicBool::new(false));
        Self {
            pending_auth_callback: Arc::new(Mutex::new(None)),
            session_origins: Arc::new(Mutex::new(SessionOrigins {
                api_origin: DEFAULT_API_ORIGIN.to_string(),
                logto_issuer: None,
            })),
            shortcuts: Arc::new(Mutex::new(ShortcutService::new(
                PlatformShortcutBackend::current(),
            ))),
            shortcuts_ready: Arc::new(AtomicBool::new(false)),
            shortcut_listener_failed,
            shortcut_listener_retry: Arc::new((Mutex::new(0), Condvar::new())),
            diagnostics_export_generation: Arc::new(Mutex::new(0)),
        }
    }
}

pub(crate) fn shortcut_status(state: &NativeBridgeState, error: Option<ShortcutFailure>) -> Value {
    let shortcuts = state
        .shortcuts
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let error = error.or_else(|| {
        state
            .shortcut_listener_failed
            .load(Ordering::SeqCst)
            .then_some(ShortcutFailure::RegistrationFailed)
    });
    json!({ "kind": "shortcut-status", "platform": shortcuts.platform(), "permission": shortcuts.permission(), "bindings": shortcuts.active(), "error": error })
}

fn apply_shortcuts(request: &Value, state: &NativeBridgeState) -> Result<Value, String> {
    let bindings = shortcut_bindings(request)?;
    let mut shortcuts = state
        .shortcuts
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    match shortcuts.apply(bindings) {
        Ok(()) => {
            state.shortcuts_ready.store(true, Ordering::SeqCst);
            drop(shortcuts);
            Ok(shortcut_status(state, None))
        }
        Err(error) => Ok(
            json!({ "kind": "shortcut-status", "platform": shortcuts.platform(), "permission": shortcuts.permission(), "bindings": shortcuts.active(), "error": error }),
        ),
    }
}

fn shortcut_bindings(request: &Value) -> Result<ShortcutBindings, String> {
    serde_json::from_value(request.get("bindings").cloned().ok_or("invalid-argument")?)
        .map_err(|_| "invalid-argument".to_string())
}

fn stage_shortcuts(request: &Value, state: &NativeBridgeState) -> Result<Value, String> {
    let bindings = shortcut_bindings(request)?;
    if state.shortcut_listener_failed.load(Ordering::SeqCst) {
        return Ok(shortcut_status(
            state,
            Some(ShortcutFailure::RegistrationFailed),
        ));
    }
    let mut shortcuts = state
        .shortcuts
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    match shortcuts.stage(bindings) {
        Ok(()) => Ok(
            json!({ "kind": "shortcut-status", "platform": shortcuts.platform(), "permission": shortcuts.permission(), "bindings": shortcuts.active(), "error": Value::Null }),
        ),
        Err(error) => Ok(
            json!({ "kind": "shortcut-status", "platform": shortcuts.platform(), "permission": shortcuts.permission(), "bindings": shortcuts.active(), "error": error }),
        ),
    }
}

fn commit_staged_shortcuts(request: &Value, state: &NativeBridgeState) -> Result<Value, String> {
    let bindings = shortcut_bindings(request)?;
    let mut shortcuts = state
        .shortcuts
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    match shortcuts.commit_staged(&bindings) {
        Ok(()) => {
            state.shortcuts_ready.store(true, Ordering::SeqCst);
            Ok(
                json!({ "kind": "shortcut-status", "platform": shortcuts.platform(), "permission": shortcuts.permission(), "bindings": shortcuts.active(), "error": Value::Null }),
            )
        }
        Err(error) => Ok(
            json!({ "kind": "shortcut-status", "platform": shortcuts.platform(), "permission": shortcuts.permission(), "bindings": shortcuts.active(), "error": error }),
        ),
    }
}

fn rollback_staged_shortcuts(state: &NativeBridgeState) -> Value {
    let mut shortcuts = state
        .shortcuts
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    match shortcuts.rollback_staged() {
        Ok(()) => {
            json!({ "kind": "shortcut-status", "platform": shortcuts.platform(), "permission": shortcuts.permission(), "bindings": shortcuts.active(), "error": Value::Null })
        }
        Err(error) => {
            json!({ "kind": "shortcut-status", "platform": shortcuts.platform(), "permission": shortcuts.permission(), "bindings": shortcuts.active(), "error": error })
        }
    }
}

fn suspend_shortcuts(state: &NativeBridgeState) -> Value {
    state.shortcuts_ready.store(false, Ordering::SeqCst);
    state.clear_shortcut_pressed_keys();
    shortcut_status(state, None)
}

impl NativeBridgeState {
    #[cfg(desktop)]
    pub fn process_shortcut_event(&self, event: NativeKeyEvent) -> Option<ShortcutAction> {
        if !self.shortcuts_ready.load(Ordering::SeqCst)
            || self.shortcut_listener_failed.load(Ordering::SeqCst)
        {
            return None;
        }
        self.shortcuts
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .process(event)
    }

    #[cfg(desktop)]
    pub fn mark_shortcut_listener_failed(&self) {
        self.shortcut_listener_failed.store(true, Ordering::SeqCst);
        self.clear_shortcut_pressed_keys();
    }

    #[cfg(desktop)]
    pub fn clear_shortcut_listener_failure(&self) {
        self.shortcut_listener_failed.store(false, Ordering::SeqCst);
    }

    pub fn clear_shortcut_pressed_keys(&self) {
        self.shortcuts
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .clear_pressed_keys();
    }

    #[cfg(desktop)]
    pub fn shortcut_listener_retry_generation(&self) -> u64 {
        let (generation, _) = &*self.shortcut_listener_retry;
        *generation
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }

    #[cfg(desktop)]
    pub fn wait_for_shortcut_listener_retry(&self, observed_generation: u64, timeout: Duration) {
        let (generation, ready) = &*self.shortcut_listener_retry;
        let generation = generation
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        let _ = ready
            .wait_timeout_while(generation, timeout, |current| {
                *current == observed_generation
            })
            .unwrap_or_else(std::sync::PoisonError::into_inner);
    }

    pub fn retry_shortcut_listener(&self) {
        let (generation, ready) = &*self.shortcut_listener_retry;
        let mut generation = generation
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        *generation = generation.wrapping_add(1);
        ready.notify_all();
    }

    #[cfg(desktop)]
    fn begin_diagnostics_export(&self) -> u64 {
        *self
            .diagnostics_export_generation
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }

    #[cfg(desktop)]
    fn persist_diagnostics_export(
        &self,
        generation: u64,
        destination: &std::path::Path,
        contents: &str,
    ) -> Result<bool, String> {
        let current_generation = self
            .diagnostics_export_generation
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if *current_generation != generation {
            return Ok(false);
        }
        let parent = destination.parent().ok_or("storage-failure")?;
        let mut file = tempfile::NamedTempFile::new_in(parent).map_err(|_| "storage-failure")?;
        file.write_all(contents.as_bytes())
            .map_err(|_| "storage-failure")?;
        file.as_file().sync_all().map_err(|_| "storage-failure")?;
        file.persist(destination).map_err(|_| "storage-failure")?;
        Ok(true)
    }

    #[cfg(desktop)]
    fn with_invalidated_diagnostics_exports<T>(
        &self,
        action: impl FnOnce() -> Result<T, String>,
    ) -> Result<T, String> {
        let mut generation = self
            .diagnostics_export_generation
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        *generation = generation.wrapping_add(1);
        action()
    }

    #[allow(dead_code)]
    pub fn offer_auth_callback(&self, candidate: &str) -> bool {
        if !is_auth_callback(candidate) {
            return false;
        }
        let mut pending = self
            .pending_auth_callback
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        *pending = Some(candidate.to_string());
        true
    }

    fn take_auth_callback(&self) -> Option<String> {
        self.pending_auth_callback
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .take()
    }

    fn peek_auth_callback(&self) -> Option<String> {
        self.pending_auth_callback
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .clone()
    }

    pub fn session_csp(&self, development: bool) -> String {
        let origins = self
            .session_origins
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        let mut connect = vec![
            "'self'".to_string(),
            origins.api_origin.clone(),
            "https://api.github.com".to_string(),
        ];
        if let Some(issuer) = &origins.logto_issuer {
            connect.push(issuer.origin().ascii_serialization());
        }
        if development {
            connect.push("ws://127.0.0.1:46305".to_string());
        }
        let style = if development {
            "'self' 'unsafe-inline'"
        } else {
            "'self'"
        };
        format!(
            "default-src 'self'; script-src 'self'; style-src {style}; img-src 'self' data:; \
             font-src 'self'; connect-src {}; object-src 'none'; base-uri 'none'; form-action \
             'none'; frame-src 'none'; worker-src 'none'",
            connect.join(" ")
        )
    }

    fn configure_session_origins(&self, request: &Value) -> Result<bool, String> {
        let api_origin = request
            .get("apiOrigin")
            .and_then(Value::as_str)
            .ok_or("invalid-argument")?;
        let api_origin = validated_api_origin(api_origin)?;
        let mut origins = self
            .session_origins
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        let logto_issuer = match request.get("logtoIssuer") {
            Some(Value::String(value)) => Some(validated_logto_issuer(value)?),
            Some(Value::Null) => None,
            Some(_) => return Err("invalid-argument".to_string()),
            None if origins.api_origin == api_origin => origins.logto_issuer.clone(),
            None => None,
        };
        let next = SessionOrigins {
            api_origin,
            logto_issuer,
        };
        let changed = *origins != next;
        *origins = next;
        Ok(changed)
    }
}

fn validated_api_origin(value: &str) -> Result<String, String> {
    let url = url::Url::parse(value).map_err(|_| "invalid-argument")?;
    let loopback = url.host_str().is_some_and(|host| {
        host == "localhost"
            || host
                .trim_matches(['[', ']'])
                .parse::<std::net::IpAddr>()
                .is_ok_and(|ip| ip.is_loopback())
    });
    if !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || url.path() != "/"
        || (url.scheme() != "https" && !(url.scheme() == "http" && loopback))
    {
        return Err("invalid-argument".to_string());
    }
    Ok(url.origin().ascii_serialization())
}

fn validated_logto_issuer(value: &str) -> Result<url::Url, String> {
    if value.trim() != value {
        return Err("invalid-argument".to_string());
    }
    let url = url::Url::parse(value).map_err(|_| "invalid-argument")?;
    let loopback = url.host_str().is_some_and(|host| {
        host.eq_ignore_ascii_case("localhost")
            || host
                .trim_matches(['[', ']'])
                .parse::<std::net::IpAddr>()
                .is_ok_and(|ip| ip.is_loopback())
    });
    if !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || (url.scheme() != "https" && !(url.scheme() == "http" && loopback))
    {
        return Err("invalid-argument".to_string());
    }
    Ok(url)
}

fn destination_is_within_issuer_path(issuer: &url::Url, destination: &url::Url) -> bool {
    let issuer_path = issuer.path().trim_end_matches('/');
    issuer_path.is_empty()
        || destination.path() == issuer_path
        || destination
            .path()
            .strip_prefix(issuer_path)
            .is_some_and(|suffix| suffix.starts_with('/'))
}

fn is_profile_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= PROFILE_ID_LIMIT
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
}

pub fn is_auth_callback(value: &str) -> bool {
    if value.trim() != value || !value.starts_with("devhud://") {
        return false;
    }
    let Ok(url) = url::Url::parse(value) else {
        return false;
    };
    url.scheme() == "devhud"
        && url.host_str() == Some("auth")
        && url.path() == "/callback"
        && url.port().is_none()
        && url.username().is_empty()
        && url.password().is_none()
        && url.fragment().is_none()
}

fn validate_secure_request(request: &Value) -> Result<(), String> {
    let setting = request.get("setting").ok_or("invalid-argument")?;
    let kind = setting
        .get("kind")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    if !matches!(
        kind,
        "logto-session" | "github-pat" | "r2-access-key-id" | "r2-secret-access-key"
    ) {
        return Err("invalid-argument".to_string());
    }
    let profile_id = setting
        .get("profileId")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    if !is_profile_id(profile_id) {
        return Err("invalid-argument".to_string());
    }
    if kind == "github-pat"
        && !setting
            .get("scopeId")
            .and_then(Value::as_str)
            .is_some_and(is_profile_id)
    {
        return Err("invalid-argument".to_string());
    }
    if let Some(value) = request.get("value").and_then(Value::as_str)
        && value.len() > SECRET_LIMIT
    {
        return Err("invalid-argument".to_string());
    }
    Ok(())
}

fn validate_github_pat_reconciliation(request: &Value) -> Result<(), String> {
    let scope_id = request
        .get("scopeId")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    if !is_profile_id(scope_id) {
        return Err("invalid-argument".to_string());
    }
    let profile_ids = request
        .get("profileIds")
        .and_then(Value::as_array)
        .ok_or("invalid-argument")?;
    if profile_ids.len() > 25 {
        return Err("invalid-argument".to_string());
    }
    let mut unique = std::collections::BTreeSet::new();
    for profile_id in profile_ids {
        let profile_id = profile_id.as_str().ok_or("invalid-argument")?;
        if !is_profile_id(profile_id) || !unique.insert(profile_id) {
            return Err("invalid-argument".to_string());
        }
    }
    Ok(())
}

fn validate_external_request(request: &Value) -> Result<(), String> {
    let target = request
        .get("target")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    if target == "fine-grained-pat" || target == "classic-pat" {
        return Ok(());
    }
    if target != "authentication" {
        return Err("invalid-argument".to_string());
    }
    let origin = request
        .get("apiOrigin")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    let url = url::Url::parse(origin).map_err(|_| "invalid-argument")?;
    let loopback = url.host_str().is_some_and(|host| {
        host == "localhost"
            || host
                .trim_matches(['[', ']'])
                .parse::<std::net::IpAddr>()
                .is_ok_and(|ip| ip.is_loopback())
    });
    if !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || url.path() != "/"
        || (url.scheme() != "https" && !(url.scheme() == "http" && loopback))
    {
        return Err("invalid-argument".to_string());
    }
    Ok(())
}

fn validate_auth_browser_request(request: &Value, state: &NativeBridgeState) -> Result<(), String> {
    let issuer = request
        .get("issuer")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    let destination = request
        .get("url")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    let issuer = validated_logto_issuer(issuer)?;
    let destination = url::Url::parse(destination).map_err(|_| "invalid-argument")?;
    let configured_issuer = state
        .session_origins
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
        .logto_issuer
        .clone();
    if configured_issuer.as_ref() != Some(&issuer)
        || destination.origin() != issuer.origin()
        || !destination_is_within_issuer_path(&issuer, &destination)
        || !destination.username().is_empty()
        || destination.password().is_some()
        || destination.fragment().is_some()
    {
        return Err("invalid-argument".to_string());
    }
    Ok(())
}

fn validate_purge_request(request: &Value) -> Result<(), String> {
    match request.get("scope").and_then(Value::as_str) {
        Some("logout") => Ok(()),
        Some("account-deletion" | "api-change") => {
            let profile = request
                .get("profileId")
                .and_then(Value::as_str)
                .ok_or("invalid-argument")?;
            if is_profile_id(profile) {
                Ok(())
            } else {
                Err("invalid-argument".to_string())
            }
        }
        _ => Err("invalid-argument".to_string()),
    }
}

fn runtime_platform() -> &'static str {
    if cfg!(target_os = "ios") {
        "ios"
    } else if cfg!(target_os = "android") {
        "android"
    } else {
        "desktop"
    }
}

fn runtime_operating_system() -> &'static str {
    if cfg!(target_os = "ios") {
        "ios"
    } else if cfg!(target_os = "android") {
        "android"
    } else if cfg!(target_os = "macos") {
        "macos"
    } else if cfg!(target_os = "windows") {
        "windows"
    } else {
        "linux"
    }
}

fn runtime_cef_revision() -> &'static str {
    #[cfg(any(target_os = "android", target_os = "ios"))]
    return "";

    #[cfg(not(any(target_os = "android", target_os = "ios")))]
    CEF_REVISION
}

fn runtime_os_version() -> String {
    os_info::get().version().to_string()
}

fn runtime_snapshot() -> Value {
    let mobile = cfg!(any(target_os = "android", target_os = "ios"));
    json!({
        "kind": "runtime",
        "snapshot": {
            "bridgeVersion": 1,
            "platform": runtime_platform(),
            "operatingSystem": runtime_operating_system(),
            "architecture": std::env::consts::ARCH,
            "osVersion": runtime_os_version(),
            "appVersion": env!("CARGO_PKG_VERSION"),
            "buildId": option_env!("DEVHUD_BUILD_ID").unwrap_or(env!("CARGO_PKG_VERSION")),
            "tauriRevision": TAURI_REVISION,
            "cefRevision": runtime_cef_revision(),
            "lifecycle": "active",
            "capabilities": {
                "secureSettings": true,
                "notifications": mobile,
                "storeUpdates": mobile,
                "widgets": false
            }
        }
    })
}

fn validate_diagnostics_export(request: &Value) -> Result<(&str, &str), String> {
    let suggested_name = request
        .get("suggestedName")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    let contents = request
        .get("contents")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    let correlation = suggested_name
        .strip_prefix("devhud-diagnostics-")
        .and_then(|value| value.strip_suffix(".json"))
        .ok_or("invalid-argument")?;
    let canonical_uuid = correlation.len() == 36
        && correlation.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte)
            }
        })
        && correlation.as_bytes().get(14) == Some(&b'7')
        && correlation
            .as_bytes()
            .get(19)
            .is_some_and(|byte| matches!(byte, b'8' | b'9' | b'a' | b'b'));
    if !canonical_uuid
        || contents.len() > DIAGNOSTICS_EXPORT_LIMIT
        || !matches!(
            serde_json::from_str::<Value>(contents),
            Ok(Value::Object(_))
        )
    {
        return Err("invalid-argument".to_string());
    }
    Ok((suggested_name, contents))
}

#[cfg(desktop)]
fn export_diagnostics(
    request: &Value,
    state: &NativeBridgeState,
    generation: u64,
) -> Result<Value, String> {
    let (suggested_name, contents) = validate_diagnostics_export(request)?;
    let Some(destination) = rfd::FileDialog::new()
        .add_filter("JSON", &["json"])
        .set_file_name(suggested_name)
        .save_file()
    else {
        return Ok(json!({ "kind": "diagnostics-export", "outcome": "cancelled" }));
    };
    if !state.persist_diagnostics_export(generation, &destination, contents)? {
        return Ok(json!({ "kind": "diagnostics-export", "outcome": "cancelled" }));
    }
    Ok(json!({ "kind": "diagnostics-export", "outcome": "saved" }))
}

#[cfg(desktop)]
fn clear_diagnostic_logs() -> Result<(), String> {
    crate::reset_diagnostic_logs()
}

fn purge_clears_diagnostics(request: &Value) -> bool {
    matches!(
        request.get("scope").and_then(Value::as_str),
        Some("logout" | "account-deletion")
    )
}

#[cfg(mobile)]
fn clear_diagnostic_logs() -> Result<(), String> {
    Ok(())
}

#[cfg(any(mobile, test))]
fn routes_to_mobile_plugin(operation: &str, android: bool) -> bool {
    operation == "diagnostics.export"
        || operation.starts_with("lifecycle.")
        || operation.starts_with("secure.")
        || operation == "auth.open-system-browser"
        || operation.starts_with("notifications.")
        || operation.starts_with("updates.")
        || (android
            && matches!(
                operation,
                "auth.peek-pending-callback" | "auth.take-pending-callback"
            ))
}

pub fn handle_native_bridge_request(
    request: &Value,
    state: &NativeBridgeState,
) -> Result<Value, String> {
    let operation = request
        .get("operation")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    match operation {
        "runtime.snapshot" => Ok(runtime_snapshot()),
        "shortcuts.status" => Ok(shortcut_status(state, None)),
        "shortcuts.request-permission" => {
            let mut shortcuts = state
                .shortcuts
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            let permission = shortcuts.request_permission();
            let platform = shortcuts.platform();
            let bindings = shortcuts.active().clone();
            drop(shortcuts);
            if permission == crate::shortcuts::ShortcutPermission::Available {
                state.retry_shortcut_listener();
            }
            Ok(
                json!({ "kind": "shortcut-status", "platform": platform, "permission": permission, "bindings": bindings, "error": Value::Null }),
            )
        }
        "shortcuts.apply" => apply_shortcuts(request, state),
        "shortcuts.stage" => stage_shortcuts(request, state),
        "shortcuts.commit" => commit_staged_shortcuts(request, state),
        "shortcuts.rollback" => Ok(rollback_staged_shortcuts(state)),
        "shortcuts.suspend" => Ok(suspend_shortcuts(state)),
        "session.configure-origins" => Ok(json!({
            "kind": "session-network-policy",
            "changed": state.configure_session_origins(request)?
        })),
        "lifecycle.open-external" => {
            validate_external_request(request)?;
            Err("unsupported".to_string())
        }
        "auth.open-system-browser" => {
            validate_auth_browser_request(request, state)?;
            Err("unsupported".to_string())
        }
        "auth.peek-pending-callback" => {
            Ok(json!({ "kind": "auth-callback", "url": state.peek_auth_callback() }))
        }
        "auth.take-pending-callback" => {
            Ok(json!({ "kind": "auth-callback", "url": state.take_auth_callback() }))
        }
        "secure.read" | "secure.write" | "secure.remove" => {
            validate_secure_request(request)?;
            if cfg!(any(target_os = "android", target_os = "ios")) {
                Err("platform-failure".to_string())
            } else {
                Err("unsupported".to_string())
            }
        }
        "secure.reconcile-github-pats" => {
            validate_github_pat_reconciliation(request)?;
            if cfg!(any(target_os = "android", target_os = "ios")) {
                Err("platform-failure".to_string())
            } else {
                Err("unsupported".to_string())
            }
        }
        "secure.purge" => {
            validate_purge_request(request)?;
            if cfg!(any(target_os = "android", target_os = "ios")) {
                Err("platform-failure".to_string())
            } else {
                Err("unsupported".to_string())
            }
        }
        "notifications.permission" => Ok(json!({
            "kind": "notification-permission",
            "permission": "not-determined"
        })),
        "notifications.request-permission"
        | "notifications.publish-deck-change"
        | "notifications.cancel-deck" => {
            Err(if cfg!(any(target_os = "android", target_os = "ios")) {
                "platform-failure"
            } else {
                "unsupported"
            }
            .to_string())
        }
        "updates.status" if cfg!(target_os = "ios") => Ok(json!({
            "kind": "update-status", "store": "app-store", "installedVersion": env!("CARGO_PKG_VERSION"), "configured": false
        })),
        "updates.status" if cfg!(target_os = "android") => Ok(json!({
            "kind": "update-status", "store": "play-store", "installedVersion": env!("CARGO_PKG_VERSION"), "configured": true
        })),
        "updates.status" | "updates.open-store" => Err("unsupported".to_string()),
        "widgets.replace-deck-snapshot" | "widgets.clear-deck-snapshot" => {
            Ok(json!({ "kind": "unsupported", "feature": "widgets" }))
        }
        _ => Err("invalid-argument".to_string()),
    }
}

#[tauri::command]
pub async fn native_bridge_v1<R: tauri::Runtime>(
    request: Value,
    state: tauri::State<'_, NativeBridgeState>,
    app: tauri::AppHandle<R>,
) -> Result<Value, String> {
    let operation = request
        .get("operation")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    if operation == "diagnostics.clear" {
        clear_diagnostic_logs()?;
        return Ok(json!({ "kind": "ok" }));
    }
    #[cfg(mobile)]
    {
        if operation == "diagnostics.export" {
            validate_diagnostics_export(&request)?;
        }
        if operation.starts_with("secure.") {
            if operation == "secure.purge" {
                validate_purge_request(&request)?;
            } else if operation == "secure.reconcile-github-pats" {
                validate_github_pat_reconciliation(&request)?;
            } else {
                validate_secure_request(&request)?;
            }
        }
        if operation == "lifecycle.open-external" {
            validate_external_request(&request)?;
        }
        if operation == "auth.open-system-browser" {
            validate_auth_browser_request(&request, &state)?;
        }
        if routes_to_mobile_plugin(operation, cfg!(target_os = "android")) {
            if operation == "secure.purge" && purge_clears_diagnostics(&request) {
                clear_diagnostic_logs()?;
            }
            return crate::native_plugin::request(&app, &request);
        }
    }
    #[cfg(desktop)]
    {
        if operation == "diagnostics.export" {
            let export_state = state.inner().clone();
            let generation = export_state.begin_diagnostics_export();
            return tauri::async_runtime::spawn_blocking(move || {
                export_diagnostics(&request, &export_state, generation)
            })
            .await
            .map_err(|_| "platform-failure".to_string())?;
        }
        if operation.starts_with("secure.") {
            if operation == "secure.purge" {
                validate_purge_request(&request)?;
            } else if operation == "secure.reconcile-github-pats" {
                validate_github_pat_reconciliation(&request)?;
            } else {
                validate_secure_request(&request)?;
            }
            if operation == "secure.purge" && purge_clears_diagnostics(&request) {
                return state.with_invalidated_diagnostics_exports(|| {
                    clear_diagnostic_logs()?;
                    crate::secure_store::handle(&request)
                });
            }
            return crate::secure_store::handle(&request);
        }
        if operation == "auth.open-system-browser" {
            validate_auth_browser_request(&request, &state)?;
            let destination = request
                .get("url")
                .and_then(Value::as_str)
                .ok_or("invalid-argument")?;
            crate::open_system_browser(destination.to_string())
                .await
                .map_err(|_| "platform-failure")?;
            return Ok(json!({ "kind": "ok" }));
        }
        let _ = app;
    }
    handle_native_bridge_request(&request, &state)
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::Ordering;

    use serde_json::json;

    use super::{
        NativeBridgeState, handle_native_bridge_request, is_auth_callback,
        purge_clears_diagnostics, routes_to_mobile_plugin, runtime_os_version, shortcut_status,
        validate_auth_browser_request, validate_diagnostics_export,
    };

    #[test]
    fn routes_pending_auth_callbacks_only_to_the_android_plugin() {
        assert!(routes_to_mobile_plugin("auth.peek-pending-callback", true));
        assert!(!routes_to_mobile_plugin(
            "auth.peek-pending-callback",
            false
        ));
        assert!(routes_to_mobile_plugin("auth.take-pending-callback", true));
        assert!(!routes_to_mobile_plugin(
            "auth.take-pending-callback",
            false
        ));
        assert!(routes_to_mobile_plugin("notifications.permission", false));
        assert!(!routes_to_mobile_plugin("runtime.snapshot", true));
    }

    #[test]
    fn diagnostics_export_accepts_only_bounded_json_and_an_app_selected_name() {
        let valid = json!({
            "suggestedName": "devhud-diagnostics-0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400.json",
            "contents": "{\"schemaVersion\":1}"
        });
        assert!(validate_diagnostics_export(&valid).is_ok());
        for invalid in [
            json!({ "suggestedName": "../../private.json", "contents": "{}" }),
            json!({ "suggestedName": "devhud-diagnostics-0198c8b0-77d6-7d4a-a7d9-e4d7b11c4400.json", "contents": "not-json" }),
        ] {
            assert_eq!(
                validate_diagnostics_export(&invalid),
                Err("invalid-argument".to_string())
            );
        }
    }

    #[test]
    fn diagnostics_cleanup_is_limited_to_destructive_purge_scopes() {
        assert!(purge_clears_diagnostics(&json!({ "scope": "logout" })));
        assert!(purge_clears_diagnostics(
            &json!({ "scope": "account-deletion", "profileId": "profile" })
        ));
        assert!(!purge_clears_diagnostics(
            &json!({ "scope": "api-change", "profileId": "profile" })
        ));
    }

    #[cfg(desktop)]
    #[test]
    fn destructive_purge_invalidation_prevents_a_pending_diagnostics_write() {
        let state = NativeBridgeState::default();
        let generation = state.begin_diagnostics_export();
        state
            .with_invalidated_diagnostics_exports(|| Ok(()))
            .expect("diagnostics export invalidation");
        let temporary = tempfile::tempdir().expect("temporary export directory");
        let destination = temporary.path().join("diagnostics.json");

        assert_eq!(
            state.persist_diagnostics_export(generation, &destination, "{}"),
            Ok(false)
        );
        assert!(!destination.exists());
    }

    #[test]
    fn runtime_snapshot_has_exact_pinned_runtime_revisions() {
        let snapshot = handle_native_bridge_request(
            &json!({ "operation": "runtime.snapshot" }),
            &NativeBridgeState::default(),
        )
        .expect("runtime snapshot");
        assert_eq!(
            snapshot["snapshot"]["tauriRevision"],
            "4af26a3f7f8b692d62cca549bbacd93f5ce90b41"
        );
        assert_eq!(snapshot["snapshot"]["osVersion"], runtime_os_version());
        assert_ne!(snapshot["snapshot"]["osVersion"], std::env::consts::OS);
        if cfg!(any(target_os = "android", target_os = "ios")) {
            assert_eq!(snapshot["snapshot"]["cefRevision"], "");
        } else {
            assert_eq!(
                snapshot["snapshot"]["cefRevision"],
                "150.0.10+g8042e43+chromium-150.0.7871.101"
            );
        }
    }

    #[test]
    fn accepts_only_the_auth_callback_route() {
        assert!(is_auth_callback("devhud://auth/callback"));
        assert!(is_auth_callback(
            "devhud://auth/callback?code=redacted&state=opaque"
        ));
        for rejected in [
            "https://auth/callback",
            "devhud://deck/123",
            "devhud://auth/other",
            "devhud://user@auth/callback",
            "devhud://auth:42/callback",
            "devhud://auth/callback#secret",
        ] {
            assert!(!is_auth_callback(rejected), "accepted {rejected}");
        }
    }

    #[test]
    fn pending_callback_is_bounded_and_consumed_once() {
        let state = NativeBridgeState::default();
        assert!(state.offer_auth_callback("devhud://auth/callback?state=one"));
        assert!(state.offer_auth_callback("devhud://auth/callback?state=two"));
        let peek = json!({ "operation": "auth.peek-pending-callback" });
        let peeked = handle_native_bridge_request(&peek, &state).expect("peek callback");
        assert_eq!(peeked["url"], "devhud://auth/callback?state=two");
        let request = json!({ "operation": "auth.take-pending-callback" });
        let first = handle_native_bridge_request(&request, &state).expect("callback");
        assert_eq!(first["url"], "devhud://auth/callback?state=two");
        let second = handle_native_bridge_request(&request, &state).expect("empty callback");
        assert!(second["url"].is_null());
    }

    #[test]
    fn native_shortcut_bindings_reject_unknown_fields() {
        let mut bindings = serde_json::to_value(crate::shortcuts::default_bindings())
            .expect("serialize default bindings");
        bindings["shell.command-palette"]["scanCode"] = json!(54);
        assert_eq!(
            handle_native_bridge_request(
                &json!({ "operation": "shortcuts.apply", "bindings": bindings }),
                &NativeBridgeState::default(),
            ),
            Err("invalid-argument".to_string())
        );
    }

    #[cfg(desktop)]
    #[test]
    fn suspending_shortcuts_blocks_matching_until_hydration_applies_bindings() {
        let state = NativeBridgeState::default();
        state.shortcuts_ready.store(true, Ordering::SeqCst);
        assert_eq!(
            state.process_shortcut_event(crate::shortcuts::NativeKeyEvent {
                key: crate::shortcuts::NativeKey::RightPrimary,
                pressed: true,
            }),
            None
        );
        let suspended =
            handle_native_bridge_request(&json!({ "operation": "shortcuts.suspend" }), &state)
                .expect("suspend shortcuts");
        assert_eq!(suspended["kind"], "shortcut-status");
        assert_eq!(
            state.process_shortcut_event(crate::shortcuts::NativeKeyEvent {
                key: crate::shortcuts::NativeKey::Key(crate::shortcuts::ShortcutKey::Digit1),
                pressed: true,
            }),
            None
        );
        state.shortcuts_ready.store(true, Ordering::SeqCst);
        assert_eq!(
            state.process_shortcut_event(crate::shortcuts::NativeKeyEvent {
                key: crate::shortcuts::NativeKey::Key(crate::shortcuts::ShortcutKey::KeyK),
                pressed: true,
            }),
            None
        );
    }

    #[cfg(desktop)]
    #[test]
    fn failed_shortcut_listener_retries_after_permission_recovery() {
        use std::{sync::mpsc, time::Duration};

        let state = NativeBridgeState::default();
        state.shortcuts_ready.store(true, Ordering::SeqCst);
        state.mark_shortcut_listener_failed();
        assert_eq!(
            shortcut_status(&state, None)["error"],
            "registration-failed"
        );
        assert_eq!(
            state.process_shortcut_event(crate::shortcuts::NativeKeyEvent {
                key: crate::shortcuts::NativeKey::Key(crate::shortcuts::ShortcutKey::Digit1),
                pressed: true,
            }),
            None
        );
        let observed_generation = state.shortcut_listener_retry_generation();
        let waiting_state = state.clone();
        let (ready, retried) = mpsc::channel();
        std::thread::spawn(move || {
            waiting_state
                .wait_for_shortcut_listener_retry(observed_generation, Duration::from_secs(1));
            ready.send(()).expect("report retry");
        });
        state.retry_shortcut_listener();
        retried
            .recv_timeout(Duration::from_secs(1))
            .expect("listener retry signal");
        state.clear_shortcut_listener_failure();
        assert!(shortcut_status(&state, None)["error"].is_null());
        assert!(
            !state
                .shortcut_listener_failed
                .load(std::sync::atomic::Ordering::SeqCst)
        );
    }

    #[cfg(desktop)]
    #[test]
    fn staging_rejects_bindings_while_the_shortcut_listener_has_failed() {
        let state = NativeBridgeState::default();
        state.mark_shortcut_listener_failed();
        let mut bindings = crate::shortcuts::default_bindings();
        bindings
            .get_mut(&crate::shortcuts::ShortcutAction::ShellCommandPalette)
            .expect("palette binding")
            .key = crate::shortcuts::ShortcutKey::KeyQ;
        let request = json!({ "operation": "shortcuts.stage", "bindings": bindings });

        let staged = handle_native_bridge_request(&request, &state).expect("stage response");
        assert_eq!(staged["error"], "registration-failed");

        state.clear_shortcut_listener_failure();
        let committed = handle_native_bridge_request(
            &json!({ "operation": "shortcuts.commit", "bindings": request["bindings"] }),
            &state,
        )
        .expect("commit response");
        assert_eq!(committed["error"], "malformed");
    }

    #[test]
    fn session_csp_contains_only_the_selected_api_and_discovered_issuer() {
        let state = NativeBridgeState::default();
        let changed = handle_native_bridge_request(
            &json!({
                "operation": "session.configure-origins",
                "apiOrigin": "https://custom.example/",
                "logtoIssuer": "https://identity.example/oidc"
            }),
            &state,
        )
        .expect("configure origins");
        assert_eq!(changed["changed"], true);
        let csp = state.session_csp(false);
        assert!(csp.contains("connect-src 'self' https://custom.example https://api.github.com https://identity.example"));
        assert!(!csp.contains("devhud.api.delino.io"));
        assert!(!csp.contains("connect-src https:"));
        assert!(!csp.contains("style-src 'self' 'unsafe-inline'"));
        assert!(
            state
                .session_csp(true)
                .contains("style-src 'self' 'unsafe-inline'")
        );

        let unchanged = handle_native_bridge_request(
            &json!({
                "operation": "session.configure-origins",
                "apiOrigin": "https://custom.example/"
            }),
            &state,
        )
        .expect("preserve discovered issuer");
        assert_eq!(unchanged["changed"], false);
        assert!(
            state
                .session_csp(false)
                .contains("https://identity.example")
        );

        let api_changed = handle_native_bridge_request(
            &json!({
                "operation": "session.configure-origins",
                "apiOrigin": "https://other.example/"
            }),
            &state,
        )
        .expect("change API and clear discovered issuer");
        assert_eq!(api_changed["changed"], true);
        let csp = state.session_csp(false);
        assert!(csp.contains("connect-src 'self' https://other.example https://api.github.com"));
        assert!(!csp.contains("identity.example"));

        assert_eq!(
            handle_native_bridge_request(
                &json!({ "operation": "session.configure-origins", "apiOrigin": "http://remote.example/" }),
                &state,
            ),
            Err("invalid-argument".to_string())
        );
    }

    #[test]
    fn authentication_browser_accepts_configured_issuer_paths_and_loopback_http() {
        for request in [
            json!({ "issuer": "https://identity.example/oidc", "url": "https://identity.example/oidc/auth?state=opaque" }),
            json!({ "issuer": "http://127.0.0.1:3001/oidc", "url": "http://127.0.0.1:3001/oidc/auth?state=opaque" }),
        ] {
            let state = NativeBridgeState::default();
            handle_native_bridge_request(
                &json!({
                    "operation": "session.configure-origins",
                    "apiOrigin": "https://api.example/",
                    "logtoIssuer": request["issuer"]
                }),
                &state,
            )
            .expect("configure issuer");
            assert_eq!(validate_auth_browser_request(&request, &state), Ok(()));
        }
        let state = NativeBridgeState::default();
        assert_eq!(
            validate_auth_browser_request(
                &json!({ "issuer": "https://configured.example/oidc", "url": "https://configured.example/auth" }),
                &state,
            ),
            Err("invalid-argument".to_string())
        );
        assert_eq!(
            validate_auth_browser_request(
                &json!({ "issuer": "https://configured.example/oidc", "url": "https://configured.example/oidc-attacker/auth" }),
                &state,
            ),
            Err("invalid-argument".to_string())
        );
        assert_eq!(
            validate_auth_browser_request(
                &json!({ "issuer": "https://configured.example/unrelated", "url": "https://configured.example/unrelated/auth" }),
                &state,
            ),
            Err("invalid-argument".to_string())
        );
        handle_native_bridge_request(
            &json!({
                "operation": "session.configure-origins",
                "apiOrigin": "https://api.example/",
                "logtoIssuer": "https://configured.example/oidc"
            }),
            &state,
        )
        .expect("configure issuer");
        assert_eq!(
            validate_auth_browser_request(
                &json!({ "issuer": "https://identity.example/oidc", "url": "https://identity.example/auth" }),
                &state,
            ),
            Err("invalid-argument".to_string())
        );
        assert_eq!(
            validate_auth_browser_request(
                &json!({ "issuer": "https://configured.example/oidc", "url": "https://attacker.example/auth" }),
                &state,
            ),
            Err("invalid-argument".to_string())
        );
    }

    #[test]
    fn validates_secure_setting_names_without_echoing_values() {
        let state = NativeBridgeState::default();
        let invalid = json!({
            "operation": "secure.write",
            "setting": { "kind": "github-pat", "profileId": "../escape", "scopeId": "origin.scope" },
            "value": "secret"
        });
        assert_eq!(
            handle_native_bridge_request(&invalid, &state),
            Err("invalid-argument".to_string())
        );
    }

    #[test]
    fn validates_github_pat_reconciliation_profile_ids() {
        let state = NativeBridgeState::default();
        assert_eq!(
            handle_native_bridge_request(
                &json!({
                    "operation": "secure.reconcile-github-pats",
                    "scopeId": "origin.scope",
                    "profileIds": ["profile-one", "profile-two"]
                }),
                &state,
            ),
            Err("unsupported".to_string())
        );
        assert_eq!(
            handle_native_bridge_request(
                &json!({
                    "operation": "secure.reconcile-github-pats",
                    "scopeId": "origin.scope",
                    "profileIds": ["../escape"]
                }),
                &state,
            ),
            Err("invalid-argument".to_string())
        );
    }

    #[test]
    fn external_navigation_is_closed_and_origin_validated() {
        let state = NativeBridgeState::default();
        for request in [
            json!({ "operation": "lifecycle.open-external", "target": "authentication", "apiOrigin": "https://api.delino.io/" }),
            json!({ "operation": "lifecycle.open-external", "target": "authentication", "apiOrigin": "http://127.0.0.1:8787/" }),
            json!({ "operation": "lifecycle.open-external", "target": "fine-grained-pat", "apiOrigin": "ignored" }),
        ] {
            assert_eq!(
                handle_native_bridge_request(&request, &state),
                Err("unsupported".to_string())
            );
        }
        for request in [
            json!({ "operation": "lifecycle.open-external", "target": "issue", "apiOrigin": "https://api.delino.io/" }),
            json!({ "operation": "lifecycle.open-external", "target": "authentication", "apiOrigin": "http://example.com/" }),
            json!({ "operation": "lifecycle.open-external", "target": "authentication", "apiOrigin": "https://user@example.com/" }),
        ] {
            assert_eq!(
                handle_native_bridge_request(&request, &state),
                Err("invalid-argument".to_string())
            );
        }
    }

    #[test]
    fn future_widgets_are_explicitly_unsupported() {
        let response = handle_native_bridge_request(
            &json!({ "operation": "widgets.clear-deck-snapshot", "deckId": "deck" }),
            &NativeBridgeState::default(),
        )
        .expect("typed unsupported response");
        assert_eq!(response["kind"], "unsupported");
    }
}
