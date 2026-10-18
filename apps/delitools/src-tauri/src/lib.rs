#[cfg(all(feature = "mobile-host", any(target_os = "android", target_os = "ios")))]
#[tauri::mobile_entry_point]
pub fn run() {
    tracing_subscriber::fmt()
        .with_target(false)
        .with_ansi(std::env::var_os("NO_COLOR").is_none())
        .init();
    tracing::info!(event = "mobile_starting");
    let result = tauri::Builder::default()
        .runtime(tauri_runtime_wry::Wry::default())
        .setup(|app| {
            tauri::WebviewWindowBuilder::new(
                app,
                "main",
                tauri::WebviewUrl::App("index.html".into()),
            )
            .title("DeliTools")
            .build()?;
            tracing::info!(event = "mobile_window_ready");
            Ok(())
        })
        .run(tauri::generate_context!());
    if let Err(error) = result {
        tracing::error!(event = "mobile_failed", %error);
        panic!("DeliTools mobile host failed: {error}");
    }
    tracing::info!(event = "mobile_stopped");
}
