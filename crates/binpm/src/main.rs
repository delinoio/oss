use binpm::{cli::Cli, error::BinpmError, logging, run_cli};
use swc_malloc as _;

fn main() {
    logging::init_logging();
    let cli = Cli::parse_args();

    match run_cli(cli) {
        Ok(code) => std::process::exit(code),
        Err(error) => exit_with_error(error),
    }
}

fn exit_with_error(error: BinpmError) -> ! {
    eprintln!("binpm error: {error}");
    std::process::exit(error.exit_code());
}
