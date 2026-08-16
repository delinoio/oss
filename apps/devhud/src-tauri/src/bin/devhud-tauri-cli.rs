fn main() {
    let args = std::env::args_os().skip(1);
    tauri_cli::run(args, Some("devhud-tauri".to_string()));
}
