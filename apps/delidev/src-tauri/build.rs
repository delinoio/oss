fn main() {
    #[cfg(feature = "desktop-host")]
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("macos") {
        build_widget_bridge();
    }
    #[cfg(feature = "desktop-host")]
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&[
            "account_oauth_native",
            "desktop_update_context",
            "desktop_update_native",
            "desktop_credential_access",
            "choose_repository_folder",
            "export_generated_image",
            "read_generated_image_export",
            "read_runner_device_preferences",
            "update_runner_device_preferences",
            "read_session_creation_preferences",
            "update_session_creation_preferences",
            "read_appearance",
            "read_sidebar_preference",
            "update_sidebar_preference",
            "read_date_format",
            "update_date_format",
            "shortcut_capture_native",
            "browser_tab_shortcuts",
            "read_shortcut_preferences",
            "update_shortcut_preferences",
            "update_appearance",
            "read_language",
            "update_language",
            "open_github",
            "open_provider_guidance",
            "connect_local",
            "launch_local",
            "retry_local",
            "inspect_local_registration",
            "recover_local_registration",
            "local_server_status",
            "local_worker_proof",
            "local_worker_control",
            "worker_network_control",
            "connection_context",
            "saved_connections",
            "removed_connections",
            "remove_connection",
            "retained_worker_control",
            "pair_connection",
            "retry_connection",
            "rename_connection",
            "open_connection",
            "connect_saved",
            "saved_worker_proof",
            "saved_worker_control",
            "show_connection_manager",
            "watch_tray_status",
            "read_tray_status",
            "activate_tray_status",
            "dismiss_tray_status",
            "present_quit_attempt",
            "read_quit_attempt",
            "observe_quit_attempt",
            "decide_quit_attempt",
            "begin_tray",
            "publish_tray",
            "read_tray_action",
            "acknowledge_tray_action",
            "begin_notifications",
            "end_notifications",
            "notification_permission",
            "request_notification_permission",
            "present_notification",
        ]),
    ))
    .expect("DeliDev native build configuration must be valid");
}

#[cfg(feature = "desktop-host")]
fn build_widget_bridge() {
    use std::{path::PathBuf, process::Command};
    let output = PathBuf::from(std::env::var_os("OUT_DIR").unwrap());
    let arch = match std::env::var("CARGO_CFG_TARGET_ARCH").unwrap().as_str() {
        "aarch64" => "arm64",
        "x86_64" => "x86_64",
        _ => panic!("Unsupported widget architecture"),
    };
    let sdk = Command::new("xcrun")
        .args(["--sdk", "macosx", "--show-sdk-path"])
        .output()
        .expect("Xcode is required for the widget bridge");
    assert!(sdk.status.success(), "Cannot resolve the macOS SDK");
    let sources = [
        "../macos-widget/Shared/Snapshot.swift",
        "../macos-widget/Shared/Localization.swift",
        "../macos-widget/Shared/SnapshotStore.swift",
        "../macos-widget/Bridge/Bridge.swift",
    ];
    for source in sources {
        println!("cargo:rerun-if-changed={source}");
    }
    let status = Command::new("xcrun")
        .args([
            "swiftc",
            "-emit-library",
            "-static",
            "-O",
            "-module-name",
            "DeliDevWidgetBridge",
            "-target",
            &format!("{arch}-apple-macosx13.0"),
            "-sdk",
            String::from_utf8(sdk.stdout).unwrap().trim(),
        ])
        .args(sources)
        .arg("-o")
        .arg(output.join("libDeliDevWidgetBridge.a"))
        .status()
        .expect("Cannot build the widget bridge");
    assert!(status.success(), "Widget bridge compilation failed");
    println!("cargo:rustc-link-search=native={}", output.display());
    println!("cargo:rustc-link-lib=static=DeliDevWidgetBridge");
    println!("cargo:rustc-link-search=native=/usr/lib/swift");
    println!("cargo:rustc-link-arg=-Wl,-rpath,/usr/lib/swift");
    for framework in ["Foundation", "WidgetKit"] {
        println!("cargo:rustc-link-lib=framework={framework}");
    }
    for library in [
        "swiftCore",
        "swiftFoundation",
        "swiftDispatch",
        "swift_Concurrency",
    ] {
        println!("cargo:rustc-link-lib=dylib={library}");
    }
}
