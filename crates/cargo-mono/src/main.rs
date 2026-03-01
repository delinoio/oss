use cargo_mono::{
    cli::{self, Cli, Command as CargoMonoCommand},
    commands,
    errors::CargoMonoError,
    git, logging, CargoMonoApp,
};
use tracing::info;

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
    let cli = cli::parse_from_env();
    commands::log_invocation(&cli.command, cli.output);
    run_preflight_checks(&cli)?;
    let app = CargoMonoApp::new()?;
    commands::execute(cli, &app)
}

fn run_preflight_checks(cli: &Cli) -> Result<(), CargoMonoError> {
    match &cli.command {
        CargoMonoCommand::Bump(args) => {
            ensure_clean_working_tree_preflight("cargo-mono.bump", args.allow_dirty)
        }
        CargoMonoCommand::Publish(args) => {
            ensure_clean_working_tree_preflight("cargo-mono.publish", args.allow_dirty)
        }
        CargoMonoCommand::List | CargoMonoCommand::Changed(_) => Ok(()),
    }
}

fn ensure_clean_working_tree_preflight(
    command_path: &'static str,
    allow_dirty: bool,
) -> Result<(), CargoMonoError> {
    info!(
        command_path,
        action = "preflight-clean-working-tree",
        outcome = "started",
        allow_dirty,
        "Running clean working tree preflight"
    );

    match git::ensure_clean_working_tree(allow_dirty) {
        Ok(()) => {
            info!(
                command_path,
                action = "preflight-clean-working-tree",
                outcome = "passed",
                allow_dirty,
                "Clean working tree preflight passed"
            );
            Ok(())
        }
        Err(error) => {
            info!(
                command_path,
                action = "preflight-clean-working-tree",
                outcome = "failed",
                allow_dirty,
                "Clean working tree preflight failed"
            );
            Err(error)
        }
    }
}
