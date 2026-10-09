// SPDX-License-Identifier: Apache-2.0
use serde::{Deserialize, Serialize};

#[derive(Deserialize, Serialize)]
#[serde(tag = "operation", rename_all = "kebab-case", deny_unknown_fields)]
pub enum PlatformRequest {
    ReadState {},
    Tls {
        origin: String,
    },
    WriteState {
        value: String,
    },
    Permission {},
    Notify {
        profile_id: String,
        inbox_id: String,
        language: String,
    },
}
impl PlatformRequest {
    pub fn validate(&self) -> bool {
        match self {
            Self::Tls { origin } => url::Url::parse(origin).is_ok_and(|u| {
                u.scheme() == "https"
                    && u.origin().ascii_serialization() == *origin
                    && u.username().is_empty()
                    && u.password().is_none()
            }),
            Self::WriteState { value } => {
                value.len() <= 4 * 1024 * 1024
                    && serde_json::from_str::<serde_json::Value>(value).is_ok()
            }
            Self::Notify {
                profile_id,
                inbox_id,
                language,
            } => {
                [profile_id, inbox_id].iter().all(|id| {
                    uuid::Uuid::parse_str(id)
                        .is_ok_and(|v| v.get_version_num() == 7 && v.to_string() == **id)
                }) && matches!(language.as_str(), "en" | "ko")
            }
            _ => true,
        }
    }
}
#[cfg(any(target_os = "ios", target_os = "android"))]
mod platform {
    use serde_json::Value;
    use tauri::{
        Emitter, Manager, Runtime,
        plugin::{Builder, PluginApi, PluginHandle, TauriPlugin},
    };

    use super::*;
    #[cfg(target_os = "ios")]
    tauri::ios_plugin_binding!(init_plugin_delidev_mobile_native);
    pub struct Bridge<R: Runtime>(PluginHandle<R>);
    #[tauri::command]
    pub fn native_mobile_v1<R: Runtime>(
        app: tauri::AppHandle<R>,
        window: tauri::WebviewWindow<R>,
        request: PlatformRequest,
    ) -> Result<Value, String> {
        if window.label() != "main" || !request.validate() {
            return Err("invalid-argument".into());
        }
        app.state::<Bridge<R>>()
            .0
            .run_mobile_plugin("request", request)
            .map_err(|_| {
                tracing::warn!(operation = "platform-request", outcome = "failed");
                "platform-failure".into()
            })
    }
    pub fn init<R: Runtime>() -> TauriPlugin<R> {
        Builder::new("delidev-mobile-native")
            .setup(|app, api: PluginApi<R, ()>| {
                #[cfg(target_os = "android")]
                let handle = api.register_android_plugin(
                    "io.delino.delidev.mobile.bridge",
                    "DeliDevMobilePlugin",
                )?;
                #[cfg(target_os = "ios")]
                let handle = api.register_ios_plugin(init_plugin_delidev_mobile_native)?;
                app.manage(Bridge(handle));
                Ok(())
            })
            .on_event(|app, event| {
                let active = match event {
                    tauri::RunEvent::Resumed => Some(true),
                    tauri::RunEvent::WindowEvent {
                        event: tauri::WindowEvent::Resumed,
                        ..
                    } => Some(true),
                    tauri::RunEvent::WindowEvent {
                        event: tauri::WindowEvent::Suspended,
                        ..
                    } => Some(false),
                    _ => None,
                };
                if let Some(active) = active {
                    let _ = app.emit("delidev-mobile:lifecycle", active);
                }
            })
            .build()
    }
}
#[cfg(any(target_os = "ios", target_os = "android"))]
#[tauri::mobile_entry_point]
pub fn run() {
    tauri::Builder::default()
        .runtime(tauri_runtime_wry::Wry::default())
        .plugin(platform::init())
        .invoke_handler(tauri::generate_handler![platform::native_mobile_v1])
        .setup(|app| {
            tauri::WebviewWindowBuilder::new(
                app,
                "main",
                tauri::WebviewUrl::App("index.html".into()),
            )
            .title("DeliDev")
            .incognito(true)
            .on_navigation(|url| {
                matches!(url.scheme(), "tauri" | "asset")
                    || (url.scheme() == "http" && url.host_str() == Some("tauri.localhost"))
            })
            .build()?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("DeliDev mobile host failed");
}
#[cfg(not(any(target_os = "ios", target_os = "android")))]
pub fn run() {}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn platform_scope_is_closed_and_has_no_remote_execution() {
        assert!(
            serde_json::from_str::<PlatformRequest>(r#"{"operation":"start-server"}"#).is_err()
        );
        assert!(
            serde_json::from_str::<PlatformRequest>(
                r#"{"operation":"read-state","path":"private"}"#
            )
            .is_err()
        );
        assert!(
            !PlatformRequest::Notify {
                profile_id: "foreign".into(),
                inbox_id: "foreign".into(),
                language: "en".into()
            }
            .validate()
        );
    }
}
