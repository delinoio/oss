use cargo_mono::{cli::Cli, commands, errors::CargoMonoError, logging, CargoMonoApp};
use clap::Parser;

fn main() {
    logging::init_logging();

    match run() {
        Ok(code) => std::process::exit(code),
        Err(error) => {
            eprintln!("cargo-mono error: {}", error.message);
            std::process::exit(error.exit_code());
        }
    }
}

fn run() -> Result<i32, CargoMonoError> {
    let cli = Cli::parse();
    let app = CargoMonoApp::new()?;
    commands::execute(cli, &app)
}
