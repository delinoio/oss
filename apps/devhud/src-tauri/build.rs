const COMMANDS: &[&str] = &[
    "get_runtime_info",
    "read_settings",
    "write_settings",
    "read_shortcut_effective_state",
    "write_shortcut_effective_state",
    "read_widget_configuration",
    "write_widget_configuration",
    "export_diagnostics",
    "reset_dev_hud",
    "hide_hud",
    "show_settings",
    "hide_settings",
    "replace_global_shortcut",
    "set_launch_at_login",
    "complete_first_run",
    "request_update_action",
    "get_auth_session",
    "start_authentication",
    "logout_authentication",
    "realqa_list_capture_sources",
    "realqa_adjust_capture_selection",
    "realqa_begin_capture",
    "realqa_cancel_capture",
    "realqa_composer_accept_image",
    "realqa_composer_remove_image",
    "realqa_composer_reset_session",
];

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
