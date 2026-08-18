#[cfg(any(target_os = "android", target_os = "ios"))]
mod bridge;
#[cfg(any(target_os = "android", target_os = "ios"))]
mod native_plugin;
#[cfg(any(target_os = "android", target_os = "ios"))]
mod shortcuts;

#[cfg(any(target_os = "android", target_os = "ios"))]
#[cfg_attr(
    any(target_os = "android", target_os = "ios"),
    tauri::mobile_entry_point
)]
pub fn run() {
    let bridge_state = bridge::NativeBridgeState::default();
    let session_network_policy = bridge_state.clone();
    tauri::Builder::default()
        .plugin(native_plugin::init())
        .manage(bridge_state)
        .invoke_handler(tauri::generate_handler![bridge::native_bridge_v1])
        .setup(move |app| {
            tauri::WebviewWindowBuilder::new(
                app,
                "main",
                tauri::WebviewUrl::App("index.html".into()),
            )
            .title("DevHUD")
            .on_web_resource_request(move |_request, response| {
                let csp = session_network_policy.session_csp(cfg!(debug_assertions));
                if let Ok(value) = csp.parse() {
                    response
                        .headers_mut()
                        .insert("Content-Security-Policy", value);
                }
            })
            .build()?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("DevHUD mobile host failed");
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
pub fn run() {}
