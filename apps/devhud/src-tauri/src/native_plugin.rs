#[cfg(mobile)]
use serde::de::DeserializeOwned;
#[cfg(mobile)]
use serde_json::Value;
#[cfg(mobile)]
use tauri::AppHandle;
#[cfg(mobile)]
use tauri::plugin::{PluginApi, PluginHandle};
use tauri::{
    Emitter, Manager, Runtime,
    plugin::{Builder, TauriPlugin},
};

#[cfg(target_os = "ios")]
tauri::ios_plugin_binding!(init_plugin_devhud_native);

#[cfg(mobile)]
pub struct NativePlatformBridge<R: Runtime>(PluginHandle<R>);

#[cfg(mobile)]
impl<R: Runtime> NativePlatformBridge<R> {
    pub fn request(&self, request: &Value) -> Result<Value, String> {
        self.0
            .run_mobile_plugin("request", request)
            .map_err(|error| {
                tracing::warn!(event = "native_bridge_request_failed", %error);
                "platform-failure".to_string()
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
