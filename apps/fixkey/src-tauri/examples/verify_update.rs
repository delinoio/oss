//! Release-only signature verification; does not install or execute a payload.
#[expect(
    clippy::print_stdout,
    clippy::print_stderr,
    reason = "Release verifier emits fixed operator-facing status codes."
)]
fn main() {
    let arguments: Vec<_> = std::env::args_os().skip(1).collect();
    if arguments.len() != 2 {
        eprintln!("fixkey-verification-arguments-invalid");
        std::process::exit(1);
    }
    let result = std::fs::read(&arguments[0])
        .ok()
        .zip(std::fs::read_to_string(&arguments[1]).ok())
        .zip(std::env::var("FIXKEY_UPDATER_PUBLIC_KEY").ok())
        .is_some_and(|((bytes, signature), key)| {
            fixkey::updates::verify_payload(&bytes, &signature, &key).is_ok()
        });
    if !result {
        eprintln!("fixkey-update-signature-invalid");
        std::process::exit(1);
    }
    println!("fixkey-update-signature-verified");
}
