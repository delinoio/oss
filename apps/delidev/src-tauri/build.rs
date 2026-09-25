fn main() {
    #[cfg(feature = "desktop-host")]
    tauri_build::try_build(
        tauri_build::Attributes::new()
            .app_manifest(tauri_build::AppManifest::new().commands(&["connect_local"])),
    )
    .expect("DeliDev native build configuration must be valid");
}
