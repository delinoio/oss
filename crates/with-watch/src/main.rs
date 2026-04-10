use clap::Parser;
use swc_malloc as _;
use with_watch::{cli::Cli, error::WithWatchError, logging, run_cli, runner::RunnerOptions};

fn main() {
    logging::init_logging();
    let cli = Cli::parse();
    let options = RunnerOptions::from_environment();

    match run_cli(cli, options) {
        Ok(code) => std::process::exit(code),
        Err(error) => exit_with_error(error),
    }
}

fn exit_with_error(error: WithWatchError) -> ! {
    eprintln!("with-watch error: {error}");
    std::process::exit(error.exit_code());
}
