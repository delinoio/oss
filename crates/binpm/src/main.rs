use binpm::{cli::Cli, error::BinpmError, logging, run_cli};
use swc_malloc as _;

fn main() {
    let cli = Cli::parse_args();
    let json = cli.json;
    logging::init_logging(cli.log_verbosity());

    match run_cli(cli) {
        Ok(code) => std::process::exit(code),
        Err(error) => exit_with_error(error, json),
    }
}

fn exit_with_error(error: BinpmError, json: bool) -> ! {
    let exit_code = error.exit_code();
    if json {
        let payload = serde_json::json!({
            "error": {
                "message": error.to_string(),
                "exit_code": exit_code,
            }
        });
        eprintln!("{payload}");
    } else {
        eprintln!("binpm error: {error}");
        if error.suggest_verbose_diagnostics() {
            eprintln!("hint: rerun with `--verbose` or `--debug` for structured diagnostics.");
        }
    }
    std::process::exit(exit_code);
}
