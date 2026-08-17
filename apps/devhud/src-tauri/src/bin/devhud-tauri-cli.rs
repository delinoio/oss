fn main() {
    let mut args = std::env::args_os().skip(1).collect::<Vec<_>>();
    // Generated Gradle/Xcode tasks invoke a Cargo-compatible command as
    // `<executable> tauri ...`; package-local commands invoke this binary
    // directly. Accept both forms without relying on a globally installed CLI.
    if args.first().is_some_and(|argument| argument == "tauri") {
        args.remove(0);
    }
    tauri_cli::run(args, Some("devhud-tauri".to_string()));
}
