fn main() {
    #[cfg(feature = "desktop-host")]
    tauri_build::build();
}
