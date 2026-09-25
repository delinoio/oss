fn main() {
    #[cfg(feature = "desktop-host")]
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&[
            "connect_local",
            "local_server_status",
            "local_worker_proof",
        ]),
    ))
    .expect("DeliDev native build configuration must be valid");
}
