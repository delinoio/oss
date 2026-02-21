use clap::Parser;
use nodeup::{cli::Cli, commands, dispatch, errors::NodeupError, logging, NodeupApp};

fn main() {
    logging::init_logging();

    match run() {
        Ok(code) => std::process::exit(code),
        Err(error) => {
            eprintln!("nodeup error: {}", error.message);
            std::process::exit(error.exit_code());
        }
    }
}

fn run() -> Result<i32, NodeupError> {
    let app = NodeupApp::new()?;

    if let Some(exit_code) = dispatch::dispatch_managed_alias_if_needed(&app)? {
        return Ok(exit_code);
    }

    let cli = Cli::parse();
    commands::execute(cli, &app)
}
