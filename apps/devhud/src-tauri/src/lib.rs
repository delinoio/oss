#[cfg(any(target_os = "android", target_os = "ios"))]
mod bridge;
#[cfg(any(target_os = "android", target_os = "ios"))]
mod native_plugin;

#[cfg(any(target_os = "android", target_os = "ios"))]
#[cfg_attr(
    any(target_os = "android", target_os = "ios"),
    tauri::mobile_entry_point
)]
pub fn run() {
    tauri::Builder::default()
        .plugin(native_plugin::init())
        .manage(bridge::NativeBridgeState::default())
        .invoke_handler(tauri::generate_handler![bridge::native_bridge_v1])
        .run(tauri::generate_context!())
        .expect("DevHUD mobile host failed");
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
pub fn run() {}
