#[cfg(mobile)]
use serde::de::DeserializeOwned;
#[cfg(mobile)]
use serde_json::Value;
#[cfg(mobile)]
use tauri::plugin::{PluginApi, PluginHandle, mobile::PluginInvokeError};
use tauri::{
    AppHandle, Emitter, Manager, Runtime,
    plugin::{Builder, TauriPlugin},
};

#[cfg(target_os = "ios")]
tauri::ios_plugin_binding!(init_plugin_devhud_native);

#[cfg(mobile)]
pub struct NativePlatformBridge<R: Runtime>(PluginHandle<R>);

#[cfg(any(mobile, test))]
fn stable_plugin_error_code(code: Option<&str>) -> &'static str {
    match code {
        Some("invalid-argument") => "invalid-argument",
        Some("permission-denied") => "permission-denied",
        Some("not-configured") => "not-configured",
        Some("unsupported") => "unsupported",
        Some("storage-failure") => "storage-failure",
        Some("platform-failure") => "platform-failure",
        _ => "platform-failure",
    }
}

#[cfg(mobile)]
fn translate_plugin_error(error: &PluginInvokeError) -> String {
    let code = match error {
        PluginInvokeError::InvokeRejected(response) => response.code.as_deref(),
        _ => None,
    };
    stable_plugin_error_code(code).to_string()
}

#[cfg(mobile)]
impl<R: Runtime> NativePlatformBridge<R> {
    pub fn request(&self, request: &Value) -> Result<Value, String> {
        self.0
            .run_mobile_plugin("request", request)
            .map_err(|error| {
                tracing::warn!(event = "native_bridge_request_failed");
                translate_plugin_error(&error)
            })
    }
}

#[cfg(mobile)]
fn initialize_mobile<R: Runtime, C: DeserializeOwned>(
    _app: &AppHandle<R>,
    api: PluginApi<R, C>,
) -> Result<NativePlatformBridge<R>, Box<dyn std::error::Error>> {
    #[cfg(target_os = "android")]
    let handle = api.register_android_plugin("io.delino.devhud.bridge", "DevhudNativePlugin")?;
    #[cfg(target_os = "ios")]
    let handle = api.register_ios_plugin(init_plugin_devhud_native)?;
    Ok(NativePlatformBridge(handle))
}

#[cfg(desktop)]
fn restore_main_window<R: Runtime>(app: &AppHandle<R>) {
    let Some(window) = app.get_webview_window("main") else {
        tracing::error!(event = "shortcut_window_restore_missing");
        return;
    };
    if window.unminimize().is_err() {
        tracing::error!(event = "shortcut_window_restore_unminimize_failed");
    }
    if window.show().is_err() {
        tracing::error!(event = "shortcut_window_restore_show_failed");
    }
    if window.set_focus().is_err() {
        tracing::error!(event = "shortcut_window_restore_focus_failed");
    }
}

#[cfg(desktop)]
fn emit_shortcut_status<R: Runtime>(app: &AppHandle<R>, state: &crate::bridge::NativeBridgeState) {
    let mut status = crate::bridge::shortcut_status(state, None);
    status["version"] = serde_json::json!(1);
    let _ = app.emit("devhud:native-event:v1", status);
}

#[cfg(desktop)]
fn dispatch_shortcut_event<R: Runtime>(
    app: &AppHandle<R>,
    event: crate::shortcuts::NativeKeyEvent,
) {
    let Some(state) = app.try_state::<crate::bridge::NativeBridgeState>() else {
        return;
    };
    let Some(action) = state.process_shortcut_event(event) else {
        return;
    };
    if action.requires_visible_window() {
        restore_main_window(app);
    }
    let Ok(action) = serde_json::to_value(action) else {
        return;
    };
    let _ = app.emit(
        "devhud:native-event:v1",
        serde_json::json!({ "version": 1, "kind": "shortcut-triggered", "action": action }),
    );
}

#[cfg(target_os = "macos")]
fn listen_for_shortcuts<R: Runtime>(app: &AppHandle<R>) -> crate::shortcuts::ShortcutFailure {
    let callback_app = app.clone();
    crate::macos_shortcut_listener::listen(move |event| {
        dispatch_shortcut_event(&callback_app, event);
    })
}

#[cfg(any(target_os = "windows", target_os = "linux"))]
fn listen_for_shortcuts<R: Runtime>(app: &AppHandle<R>) -> crate::shortcuts::ShortcutFailure {
    let callback_app = app.clone();
    let _ = rdev::listen(move |event| {
        let Some(event) = crate::shortcuts::normalize_global_event(&event.event_type) else {
            return;
        };
        dispatch_shortcut_event(&callback_app, event);
    });
    crate::shortcuts::ShortcutFailure::RegistrationFailed
}

#[cfg(all(
    desktop,
    not(any(target_os = "macos", target_os = "windows", target_os = "linux"))
))]
fn listen_for_shortcuts<R: Runtime>(_app: &AppHandle<R>) -> crate::shortcuts::ShortcutFailure {
    crate::shortcuts::ShortcutFailure::RegistrationFailed
}

#[cfg(desktop)]
fn next_shortcut_listener_retry_delay(current: std::time::Duration) -> std::time::Duration {
    current
        .saturating_mul(2)
        .min(std::time::Duration::from_secs(5))
}

#[cfg(desktop)]
fn install_global_shortcut_listener<R: Runtime>(app: &AppHandle<R>) {
    let app = app.clone();
    std::thread::spawn(move || {
        let mut retry_delay = std::time::Duration::from_millis(250);
        loop {
            let failure = listen_for_shortcuts(&app);
            let Some(state) = app.try_state::<crate::bridge::NativeBridgeState>() else {
                return;
            };
            state.mark_shortcut_listener_failed(failure);
            emit_shortcut_status(&app, &state);
            tracing::warn!(event = "shortcut_listener_failed");
            let retry_generation = state.shortcut_listener_retry_generation();
            state.wait_for_shortcut_listener_retry(retry_generation, retry_delay);
            state.clear_shortcut_pressed_keys();
            state.clear_shortcut_listener_failure();
            emit_shortcut_status(&app, &state);
            retry_delay = next_shortcut_listener_retry_delay(retry_delay);
        }
    });
}

pub fn init<R: Runtime>() -> TauriPlugin<R> {
    Builder::new("devhud-native")
        .setup(|app, api| {
            #[cfg(mobile)]
            app.manage(initialize_mobile(app, api)?);
            #[cfg(desktop)]
            {
                let _ = api;
                install_global_shortcut_listener(app);
            }
            Ok(())
        })
        .on_event(|app, event| {
            let lifecycle = match event {
                tauri::RunEvent::Resumed => Some("active"),
                #[cfg(mobile)]
                tauri::RunEvent::WindowEvent {
                    event: tauri::WindowEvent::Suspended,
                    ..
                } => Some("background"),
                #[cfg(mobile)]
                tauri::RunEvent::WindowEvent {
                    event: tauri::WindowEvent::Resumed,
                    ..
                } => Some("active"),
                _ => None,
            };
            if let Some(state) = lifecycle {
                let _ = app.emit(
                    "devhud:native-event:v1",
                    serde_json::json!({ "version": 1, "kind": "lifecycle", "state": state }),
                );
            }
            #[cfg(mobile)]
            if let tauri::RunEvent::Opened { urls } = event {
                for url in urls {
                    offer_auth_callback(app, url.as_str());
                    offer_deck_link(app, url.as_str());
                }
            }
        })
        .build()
}

pub fn offer_auth_callback<R: Runtime>(app: &AppHandle<R>, candidate: &str) {
    if !crate::bridge::is_auth_callback(candidate) {
        return;
    }
    if let Some(state) = app.try_state::<crate::bridge::NativeBridgeState>() {
        state.offer_auth_callback(candidate);
    }
    let _ = app.emit(
        "devhud:native-event:v1",
        serde_json::json!({ "version": 1, "kind": "auth-callback", "url": candidate }),
    );
}

pub fn offer_deck_link<R: Runtime>(app: &AppHandle<R>, candidate: &str) -> bool {
    let Some(deck_id) = crate::bridge::deck_id_from_deep_link(candidate) else {
        return false;
    };
    if let Some(state) = app.try_state::<crate::bridge::NativeBridgeState>() {
        state.offer_deck_link(candidate);
    }
    let _ = app.emit(
        "devhud:native-event:v1",
        serde_json::json!({ "version": 1, "kind": "deck-link", "deckId": deck_id }),
    );
    true
}

#[cfg(mobile)]
pub fn request<R: Runtime>(app: &AppHandle<R>, value: &Value) -> Result<Value, String> {
    app.state::<NativePlatformBridge<R>>().request(value)
}

#[cfg(test)]
mod tests {
    use super::{next_shortcut_listener_retry_delay, stable_plugin_error_code};

    #[test]
    fn preserves_only_stable_native_bridge_error_codes() {
        for code in [
            "invalid-argument",
            "permission-denied",
            "not-configured",
            "unsupported",
            "storage-failure",
            "platform-failure",
        ] {
            assert_eq!(stable_plugin_error_code(Some(code)), code);
        }
        assert_eq!(
            stable_plugin_error_code(Some("native-secret")),
            "platform-failure"
        );
        assert_eq!(stable_plugin_error_code(None), "platform-failure");
    }

    #[test]
    fn shortcut_listener_retry_backoff_is_capped() {
        let mut delay = std::time::Duration::from_millis(250);
        for expected in [500, 1_000, 2_000, 4_000, 5_000, 5_000] {
            delay = next_shortcut_listener_retry_delay(delay);
            assert_eq!(delay, std::time::Duration::from_millis(expected));
        }
    }
}
