const COMMANDS: &[&str] = &["get_runtime_info"];

fn main() {
    let runtime_selected = std::env::var_os("CARGO_FEATURE_DESKTOP_CEF").is_some()
        || std::env::var_os("CARGO_FEATURE_MOBILE_SYSTEM_WEBVIEW").is_some();
    if !runtime_selected {
        println!("cargo:rerun-if-env-changed=CARGO_FEATURE_DESKTOP_CEF");
        println!("cargo:rerun-if-env-changed=CARGO_FEATURE_MOBILE_SYSTEM_WEBVIEW");
        return;
    }

    tauri_build::try_build(
        tauri_build::Attributes::new()
            .codegen(tauri_build::CodegenContext::new())
            .app_manifest(tauri_build::AppManifest::new().commands(COMMANDS)),
    )
    .expect("failed to build DevHud");
}
