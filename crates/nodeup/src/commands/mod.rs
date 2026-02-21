mod default_cmd;
mod override_cmd;
mod run_cmd;
mod show;
mod skeleton;
mod toolchain;
mod update_check;
mod which_cmd;

use serde::Serialize;
use tracing::info;

use crate::{
    cli::{Cli, Command, OutputFormat},
    errors::Result,
    types::NodeupCommand,
    NodeupApp,
};

pub fn execute(cli: Cli, app: &NodeupApp) -> Result<i32> {
    match cli.command {
        Command::Toolchain { command } => {
            info!(command_path = "nodeup.toolchain", "Running command");
            toolchain::execute(command, cli.output, app)
        }
        Command::Default { runtime } => {
            info!(command_path = "nodeup.default", "Running command");
            default_cmd::execute(runtime.as_deref(), cli.output, app)
        }
        Command::Show { command } => {
            info!(command_path = "nodeup.show", "Running command");
            show::execute(command, cli.output, app)
        }
        Command::Update { runtimes } => {
            info!(command_path = "nodeup.update", "Running command");
            update_check::update(runtimes, cli.output, app)
        }
        Command::Check => {
            info!(command_path = "nodeup.check", "Running command");
            update_check::check(cli.output, app)
        }
        Command::Override { command } => {
            info!(command_path = "nodeup.override", "Running command");
            override_cmd::execute(command, cli.output, app)
        }
        Command::Which { runtime, command } => {
            info!(command_path = "nodeup.which", "Running command");
            which_cmd::execute(runtime.as_deref(), &command, cli.output, app)
        }
        Command::Run {
            install,
            runtime,
            command,
        } => {
            info!(command_path = "nodeup.run", "Running command");
            run_cmd::execute(install, &runtime, &command, cli.output, app)
        }
        Command::SelfCmd { command } => {
            info!(command_path = "nodeup.self", "Running command");
            skeleton::self_command(command)
        }
        Command::Completions { shell, command } => {
            info!(command_path = "nodeup.completions", "Running command");
            skeleton::completions(&shell, command.as_deref())
        }
    }
}

pub fn print_output<T: Serialize>(
    output: OutputFormat,
    human_line: &str,
    json_value: &T,
) -> Result<()> {
    match output {
        OutputFormat::Human => println!("{human_line}"),
        OutputFormat::Json => println!("{}", serde_json::to_string_pretty(json_value)?),
    }

    Ok(())
}

pub fn command_key(command: NodeupCommand) -> &'static str {
    command.as_str()
}
