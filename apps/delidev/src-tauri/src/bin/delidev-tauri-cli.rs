// Keep packaging on the same immutable Tauri revision as the native host.
fn main() {
    tauri_cli::run(
        std::env::args_os().skip(1),
        Some("delidev-tauri".to_owned()),
    );
}
