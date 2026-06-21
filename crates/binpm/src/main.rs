use binpm::{cli::Cli, error::BinpmError, logging, run_cli};
use swc_malloc as _;

fn main() {
    let cli = Cli::parse_args();
    logging::init_logging(cli.log_verbosity());

    match run_cli(cli) {
        Ok(code) => std::process::exit(code),
        Err(error) => exit_with_error(error),
    }
}

fn exit_with_error(error: BinpmError) -> ! {
    eprintln!("binpm error: {error}");
    if error.suggest_verbose_diagnostics() {
        eprintln!("hint: rerun with `--verbose` or `--debug` for structured diagnostics.");
    }
    std::process::exit(error.exit_code());
}
