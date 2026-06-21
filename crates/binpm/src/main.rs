use binpm::{cli::Cli, error::BinpmError, logging, run_cli};
use swc_malloc as _;

fn main() {
    let parse_json = Cli::json_requested(std::env::args_os());
    let cli = match Cli::try_parse_args() {
        Ok(cli) => cli,
        Err(error) => exit_with_parse_error(error, parse_json),
    };
    let json = cli.json;
    if !json {
        logging::init_logging(cli.log_verbosity());
    }

    match run_cli(cli) {
        Ok(code) => std::process::exit(code),
        Err(error) => exit_with_error(error, json),
    }
}

fn exit_with_parse_error(error: clap::Error, json: bool) -> ! {
    if json {
        let exit_code = error.exit_code();
        let payload = serde_json::json!({
            "error": {
                "message": error.to_string(),
                "exit_code": exit_code,
            }
        });
        eprintln!("{payload}");
        std::process::exit(exit_code);
    }
    error.exit();
}

fn exit_with_error(error: BinpmError, json: bool) -> ! {
    let exit_code = error.exit_code();
    if json {
        let mut payload = serde_json::json!({
            "error": {
                "message": error.to_string(),
                "exit_code": exit_code,
            }
        });
        if let Some(diagnostic) = error.structured_diagnostic() {
            payload["error"]["diagnostic"] = diagnostic;
        }
        eprintln!("{payload}");
    } else {
        eprintln!("binpm error: {error}");
        if error.suggest_verbose_diagnostics() {
            eprintln!("hint: rerun with `--verbose` or `--debug` for structured diagnostics.");
        }
    }
    std::process::exit(exit_code);
}
