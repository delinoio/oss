fn main() {
    tauri_plugin::Builder::new(&["exportDiagnostics"])
        .android_path("android")
        .ios_path("ios")
        .build();
}
