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
                tracing::warn!(event = "native_bridge_request_failed", %error);
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
    if let Err(reason) = window.unminimize() {
        tracing::error!(event = "shortcut_window_restore_unminimize_failed", %reason);
    }
    if let Err(reason) = window.show() {
        tracing::error!(event = "shortcut_window_restore_show_failed", %reason);
    }
    if let Err(reason) = window.set_focus() {
        tracing::error!(event = "shortcut_window_restore_focus_failed", %reason);
    }
}

#[cfg(desktop)]
fn install_global_shortcut_listener<R: Runtime>(app: &AppHandle<R>) {
    let app = app.clone();
    std::thread::spawn(move || {
        let mut retry_delay = std::time::Duration::from_millis(250);
        loop {
            let callback_app = app.clone();
            let result = rdev::listen(move |event| {
                let Some(event) = crate::shortcuts::normalize_global_event(&event.event_type)
                else {
                    return;
                };
                let Some(state) = callback_app.try_state::<crate::bridge::NativeBridgeState>()
                else {
                    return;
                };
                let Some(action) = state.process_shortcut_event(event) else {
                    return;
                };
                if action == crate::shortcuts::ShortcutAction::ShellCommandPalette {
                    restore_main_window(&callback_app);
                }
                let Ok(action) = serde_json::to_value(action) else {
                    return;
                };
                let _ = callback_app.emit(
                    "devhud:native-event:v1",
                    serde_json::json!({ "version": 1, "kind": "shortcut-triggered", "action": action }),
                );
            });
            let Err(error) = result else {
                return;
            };
            let Some(state) = app.try_state::<crate::bridge::NativeBridgeState>() else {
                return;
            };
            state.mark_shortcut_listener_failed();
            tracing::warn!(event = "shortcut_listener_failed", ?error);
            let retry_generation = state.shortcut_listener_retry_generation();
            state.wait_for_shortcut_listener_retry(retry_generation, retry_delay);
            state.clear_shortcut_pressed_keys();
            state.clear_shortcut_listener_failure();
            retry_delay = retry_delay
                .saturating_mul(2)
                .min(std::time::Duration::from_secs(5));
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

#[cfg(mobile)]
pub fn request<R: Runtime>(app: &AppHandle<R>, value: &Value) -> Result<Value, String> {
    app.state::<NativePlatformBridge<R>>().request(value)
}

#[cfg(test)]
mod tests {
    use super::stable_plugin_error_code;

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
}
