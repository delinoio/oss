#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

#[tauri_runtime_cef::cef_entry_point]
fn main() {
    tracing_subscriber::fmt()
        .with_target(false)
        .with_ansi(std::env::var_os("NO_COLOR").is_none())
        .with_writer(std::io::stderr)
        .init();
    tracing::info!(event = "desktop_starting");
    let result = tauri::Builder::<tauri_runtime_cef::CefRuntime>::new()
        .runtime(tauri_runtime_cef::Cef::default().sandbox(if cfg!(windows) {
            // The pinned CEF runtime does not support the Windows broker yet.
            // Remove this platform exception when upstream supports it.
            tauri_runtime_cef::SandboxPolicy::Auto
        } else {
            tauri_runtime_cef::SandboxPolicy::Required
        }))
        .setup(|app| {
            tauri::WebviewWindowBuilder::new(
                app,
                "main",
                tauri::WebviewUrl::App("index.html".into()),
            )
            .title("DeliTools")
            .inner_size(1280.0, 820.0)
            .incognito(true)
            .build()?;
            tracing::info!(event = "desktop_window_ready");
            Ok(())
        })
        .run(tauri::generate_context!());
    if let Err(error) = result {
        tracing::error!(event = "desktop_failed", %error);
        std::process::exit(1);
    }
    tracing::info!(event = "desktop_stopped");
}
