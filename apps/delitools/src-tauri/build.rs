fn main() {
    #[cfg(any(feature = "desktop-host", feature = "mobile-host"))]
    tauri_build::build();
}
