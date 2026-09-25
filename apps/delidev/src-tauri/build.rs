fn main() {
    #[cfg(feature = "desktop-host")]
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&[
            "connect_local",
            "local_server_status",
            "local_worker_proof",
            "local_worker_control",
            "connection_context",
            "saved_connections",
            "pair_connection",
            "retry_connection",
            "rename_connection",
            "open_connection",
            "connect_saved",
            "saved_worker_proof",
            "saved_worker_control",
            "show_connection_manager",
        ]),
    ))
    .expect("DeliDev native build configuration must be valid");
}
