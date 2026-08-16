use std::sync::Mutex;

use serde_json::{Value, json};

const PROFILE_ID_LIMIT: usize = 128;
const SECRET_LIMIT: usize = 64 * 1024;

#[derive(Default)]
pub struct NativeBridgeState {
    pending_auth_callback: Mutex<Option<String>>,
}

impl NativeBridgeState {
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
    if let Some(value) = request.get("value").and_then(Value::as_str)
        && value.len() > SECRET_LIMIT
    {
        return Err("invalid-argument".to_string());
    }
    Ok(())
}

fn validate_external_request(request: &Value) -> Result<(), String> {
    let target = request
        .get("target")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    if target == "pat" {
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

fn runtime_platform() -> &'static str {
    if cfg!(target_os = "ios") {
        "ios"
    } else if cfg!(target_os = "android") {
        "android"
    } else {
        "desktop"
    }
}

fn runtime_snapshot() -> Value {
    let mobile = cfg!(any(target_os = "android", target_os = "ios"));
    json!({
        "kind": "runtime",
        "snapshot": {
            "bridgeVersion": 1,
            "platform": runtime_platform(),
            "architecture": std::env::consts::ARCH,
            "osVersion": "system",
            "lifecycle": "active",
            "capabilities": {
                "secureSettings": mobile,
                "notifications": mobile,
                "storeUpdates": mobile,
                "widgets": false
            }
        }
    })
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
        "lifecycle.open-external" => {
            validate_external_request(request)?;
            Err("unsupported".to_string())
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
pub fn native_bridge_v1<R: tauri::Runtime>(
    request: Value,
    state: tauri::State<'_, NativeBridgeState>,
    app: tauri::AppHandle<R>,
) -> Result<Value, String> {
    #[cfg(mobile)]
    {
        let operation = request
            .get("operation")
            .and_then(Value::as_str)
            .ok_or("invalid-argument")?;
        if operation.starts_with("secure.") {
            validate_secure_request(&request)?;
        }
        if operation == "lifecycle.open-external" {
            validate_external_request(&request)?;
        }
        if operation.starts_with("lifecycle.")
            || operation.starts_with("secure.")
            || operation.starts_with("notifications.")
            || operation.starts_with("updates.")
        {
            return crate::native_plugin::request(&app, &request);
        }
    }
    #[cfg(desktop)]
    let _ = app;
    handle_native_bridge_request(&request, &state)
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::{NativeBridgeState, handle_native_bridge_request, is_auth_callback};

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
        let request = json!({ "operation": "auth.take-pending-callback" });
        let first = handle_native_bridge_request(&request, &state).expect("callback");
        assert_eq!(first["url"], "devhud://auth/callback?state=two");
        let second = handle_native_bridge_request(&request, &state).expect("empty callback");
        assert!(second["url"].is_null());
    }

    #[test]
    fn validates_secure_setting_names_without_echoing_values() {
        let state = NativeBridgeState::default();
        let invalid = json!({
            "operation": "secure.write",
            "setting": { "kind": "github-pat", "profileId": "../escape" },
            "value": "secret"
        });
        assert_eq!(
            handle_native_bridge_request(&invalid, &state),
            Err("invalid-argument".to_string())
        );
    }

    #[test]
    fn external_navigation_is_closed_and_origin_validated() {
        let state = NativeBridgeState::default();
        for request in [
            json!({ "operation": "lifecycle.open-external", "target": "authentication", "apiOrigin": "https://api.delino.io/" }),
            json!({ "operation": "lifecycle.open-external", "target": "authentication", "apiOrigin": "http://127.0.0.1:8787/" }),
            json!({ "operation": "lifecycle.open-external", "target": "pat", "apiOrigin": "ignored" }),
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
