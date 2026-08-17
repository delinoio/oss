fn main() {
    tauri_plugin::Builder::new(&[])
        .ios_path("mobile/ios")
        .build();
    tauri_build::build();
}
