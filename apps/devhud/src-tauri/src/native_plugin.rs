#[cfg(mobile)]
use serde::de::DeserializeOwned;
#[cfg(mobile)]
use serde_json::Value;
#[cfg(mobile)]
use tauri::AppHandle;
#[cfg(mobile)]
use tauri::plugin::{PluginApi, PluginHandle, mobile::PluginInvokeError};
use tauri::{
    Emitter, Manager, Runtime,
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

pub fn init<R: Runtime>() -> TauriPlugin<R> {
    Builder::new("devhud-native")
        .setup(|app, api| {
            #[cfg(mobile)]
            app.manage(initialize_mobile(app, api)?);
            #[cfg(desktop)]
            let _ = (app, api);
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
            if let tauri::RunEvent::Opened { urls } = event {
                for url in urls {
                    let candidate = url.as_str();
                    if crate::bridge::is_auth_callback(candidate) {
                        if let Some(state) = app.try_state::<crate::bridge::NativeBridgeState>() {
                            state.offer_auth_callback(candidate);
                        }
                        let _ = app.emit(
                            "devhud:native-event:v1",
                            serde_json::json!({ "version": 1, "kind": "auth-callback", "url": candidate }),
                        );
                    }
                }
            }
        })
        .build()
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
