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
    "show_realqa",
    "hide_settings",
    "replace_global_shortcut",
    "set_launch_at_login",
    "complete_first_run",
    "request_update_action",
    "realqa_capture_permission_status",
    "realqa_request_capture_permission",
    "get_auth_session",
    "start_authentication",
    "logout_authentication",
    "realqa_inspect_capture_capabilities",
    "realqa_list_capture_sources",
    "realqa_adjust_capture_selection",
    "realqa_begin_capture",
    "realqa_begin_browser_fallback_capture",
    "realqa_cancel_capture",
    "realqa_composer_accept_image",
    "realqa_composer_flatten_image",
    "realqa_composer_remove_image",
    "realqa_composer_reset_session",
    "realqa_take_browser_capture",
    "realqa_get_local_draft_status",
    "realqa_list_local_drafts",
    "realqa_save_local_draft",
    "realqa_load_local_draft",
    "realqa_delete_local_draft",
    "realqa_assert_local_draft_submission_allowed",
    "realqa_connect",
    "realqa_signed_put",
];

fn main() {
    println!("cargo:rerun-if-env-changed=DEVHUD_CHROME_EXTENSION_ID");
    let target = std::env::var("TARGET").unwrap_or_default();
    if target.ends_with("-apple-darwin") {
        cc::Build::new()
            .file("src/realqa_capture/macos_native.m")
            .flag("-fobjc-arc")
            .flag("-fblocks")
            .flag("-mmacosx-version-min=14.0")
            .compile("devhud_realqa_macos_capture");
        println!("cargo:rustc-link-lib=framework=CoreGraphics");
        println!("cargo:rustc-link-lib=framework=Foundation");
        println!("cargo:rustc-link-lib=framework=ScreenCaptureKit");
        println!("cargo:rerun-if-changed=src/realqa_capture/macos_native.m");
    }

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
