mod default_cmd;
mod override_cmd;
mod run_cmd;
mod self_cmd;
mod show;
mod skeleton;
mod toolchain;
mod update_check;
mod which_cmd;

use serde::Serialize;
use serde_json::{json, Value};
use tracing::info;

use crate::{
    cli::{
        Cli, Command, OutputFormat, OverrideCommand, SelfCommand, ShowCommand, ToolchainCommand,
        ToolchainListDetail,
    },
    errors::Result,
    types::{
        NodeupCommand, NodeupOverrideCommand, NodeupSelfCommand, NodeupShowCommand,
        NodeupToolchainCommand,
    },
    NodeupApp,
};

pub fn execute(cli: Cli, app: &NodeupApp) -> Result<i32> {
    log_command_invocation(&cli.command, cli.output);

    match cli.command {
        Command::Toolchain { command } => toolchain::execute(command, cli.output, app),
        Command::Default { runtime } => default_cmd::execute(runtime.as_deref(), cli.output, app),
        Command::Show { command } => show::execute(command, cli.output, app),
        Command::Update { runtimes } => update_check::update(runtimes, cli.output, app),
        Command::Check => update_check::check(cli.output, app),
        Command::Override { command } => override_cmd::execute(command, cli.output, app),
        Command::Which { runtime, command } => {
            which_cmd::execute(runtime.as_deref(), &command, cli.output, app)
        }
        Command::Run {
            install,
            runtime,
            command,
        } => run_cmd::execute(install, &runtime, &command, cli.output, app),
        Command::SelfCmd { command } => self_cmd::execute(command, cli.output, app),
        Command::Completions { shell, command } => {
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

#[derive(Debug, Clone, PartialEq, Eq)]
struct CommandInvocationMetadata {
    command_path: &'static str,
    arg_shape: Value,
}

fn log_command_invocation(command: &Command, output: OutputFormat) {
    let metadata = command_invocation_metadata(command, output);
    let arg_shape = serde_json::to_string(&metadata.arg_shape).unwrap_or_else(|_| "{}".to_string());
    info!(
        command_path = metadata.command_path,
        arg_shape = %arg_shape,
        "Running command"
    );
}

fn command_invocation_metadata(
    command: &Command,
    output: OutputFormat,
) -> CommandInvocationMetadata {
    let output = output_key(output);

    match command {
        Command::Toolchain { command } => {
            let subcommand = toolchain_command(command);
            CommandInvocationMetadata {
                command_path: toolchain_command_path(subcommand),
                arg_shape: match command {
                    ToolchainCommand::List { quiet, verbose } => json!({
                        "output": output,
                        "list_format": ToolchainListDetail::from_flags(*quiet, *verbose).as_str()
                    }),
                    ToolchainCommand::Install { runtimes } => json!({
                        "output": output,
                        "runtimes_count": runtimes.len()
                    }),
                    ToolchainCommand::Uninstall { runtimes } => json!({
                        "output": output,
                        "runtimes_count": runtimes.len()
                    }),
                    ToolchainCommand::Link { name, path } => json!({
                        "output": output,
                        "name_provided": !name.is_empty(),
                        "path_provided": !path.is_empty()
                    }),
                },
            }
        }
        Command::Default { runtime } => CommandInvocationMetadata {
            command_path: "nodeup.default",
            arg_shape: json!({
                "output": output,
                "runtime_provided": runtime.is_some()
            }),
        },
        Command::Show { command } => {
            let subcommand = show_command(command);
            CommandInvocationMetadata {
                command_path: show_command_path(subcommand),
                arg_shape: json!({
                    "output": output,
                    "show_command": subcommand.as_str()
                }),
            }
        }
        Command::Update { runtimes } => CommandInvocationMetadata {
            command_path: "nodeup.update",
            arg_shape: json!({
                "output": output,
                "runtimes_count": runtimes.len()
            }),
        },
        Command::Check => CommandInvocationMetadata {
            command_path: "nodeup.check",
            arg_shape: json!({ "output": output }),
        },
        Command::Override { command } => {
            let subcommand = override_command(command);
            CommandInvocationMetadata {
                command_path: override_command_path(subcommand),
                arg_shape: match command {
                    OverrideCommand::List => json!({ "output": output }),
                    OverrideCommand::Set { path, .. } => json!({
                        "output": output,
                        "path_provided": path.is_some()
                    }),
                    OverrideCommand::Unset { path, nonexistent } => json!({
                        "output": output,
                        "path_provided": path.is_some(),
                        "nonexistent": nonexistent
                    }),
                },
            }
        }
        Command::Which { runtime, command } => CommandInvocationMetadata {
            command_path: "nodeup.which",
            arg_shape: json!({
                "output": output,
                "runtime_provided": runtime.is_some(),
                "command_provided": !command.is_empty()
            }),
        },
        Command::Run {
            install,
            runtime,
            command,
        } => CommandInvocationMetadata {
            command_path: "nodeup.run",
            arg_shape: json!({
                "output": output,
                "install": install,
                "runtime_provided": !runtime.is_empty(),
                "delegated_argv_len": command.len()
            }),
        },
        Command::SelfCmd { command } => {
            let subcommand = self_command(command);
            CommandInvocationMetadata {
                command_path: self_command_path(subcommand),
                arg_shape: json!({
                    "output": output,
                    "action": subcommand.as_str()
                }),
            }
        }
        Command::Completions { shell, command } => CommandInvocationMetadata {
            command_path: "nodeup.completions",
            arg_shape: json!({
                "output": output,
                "shell": shell,
                "command_scope_provided": command.is_some()
            }),
        },
    }
}

fn output_key(output: OutputFormat) -> &'static str {
    match output {
        OutputFormat::Human => "human",
        OutputFormat::Json => "json",
    }
}

fn toolchain_command(command: &ToolchainCommand) -> NodeupToolchainCommand {
    match command {
        ToolchainCommand::List { .. } => NodeupToolchainCommand::List,
        ToolchainCommand::Install { .. } => NodeupToolchainCommand::Install,
        ToolchainCommand::Uninstall { .. } => NodeupToolchainCommand::Uninstall,
        ToolchainCommand::Link { .. } => NodeupToolchainCommand::Link,
    }
}

fn toolchain_command_path(command: NodeupToolchainCommand) -> &'static str {
    match command {
        NodeupToolchainCommand::List => "nodeup.toolchain.list",
        NodeupToolchainCommand::Install => "nodeup.toolchain.install",
        NodeupToolchainCommand::Uninstall => "nodeup.toolchain.uninstall",
        NodeupToolchainCommand::Link => "nodeup.toolchain.link",
    }
}

fn show_command(command: &ShowCommand) -> NodeupShowCommand {
    match command {
        ShowCommand::ActiveRuntime => NodeupShowCommand::ActiveRuntime,
        ShowCommand::Home => NodeupShowCommand::Home,
    }
}

fn show_command_path(command: NodeupShowCommand) -> &'static str {
    match command {
        NodeupShowCommand::ActiveRuntime => "nodeup.show.active-runtime",
        NodeupShowCommand::Home => "nodeup.show.home",
    }
}

fn override_command(command: &OverrideCommand) -> NodeupOverrideCommand {
    match command {
        OverrideCommand::List => NodeupOverrideCommand::List,
        OverrideCommand::Set { .. } => NodeupOverrideCommand::Set,
        OverrideCommand::Unset { .. } => NodeupOverrideCommand::Unset,
    }
}

fn override_command_path(command: NodeupOverrideCommand) -> &'static str {
    match command {
        NodeupOverrideCommand::List => "nodeup.override.list",
        NodeupOverrideCommand::Set => "nodeup.override.set",
        NodeupOverrideCommand::Unset => "nodeup.override.unset",
    }
}

fn self_command(command: &SelfCommand) -> NodeupSelfCommand {
    match command {
        SelfCommand::Update => NodeupSelfCommand::Update,
        SelfCommand::Uninstall => NodeupSelfCommand::Uninstall,
        SelfCommand::UpgradeData => NodeupSelfCommand::UpgradeData,
    }
}

fn self_command_path(command: NodeupSelfCommand) -> &'static str {
    match command {
        NodeupSelfCommand::Update => "nodeup.self.update",
        NodeupSelfCommand::Uninstall => "nodeup.self.uninstall",
        NodeupSelfCommand::UpgradeData => "nodeup.self.upgrade-data",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn command_invocation_metadata_covers_all_command_paths() {
        let cases = vec![
            (
                Command::Toolchain {
                    command: ToolchainCommand::List {
                        quiet: false,
                        verbose: false,
                    },
                },
                OutputFormat::Human,
                "nodeup.toolchain.list",
                json!({
                    "output": "human",
                    "list_format": "standard"
                }),
            ),
            (
                Command::Toolchain {
                    command: ToolchainCommand::Install {
                        runtimes: vec!["lts".to_string(), "22.1.0".to_string()],
                    },
                },
                OutputFormat::Json,
                "nodeup.toolchain.install",
                json!({ "output": "json", "runtimes_count": 2 }),
            ),
            (
                Command::Toolchain {
                    command: ToolchainCommand::Uninstall {
                        runtimes: vec!["22.1.0".to_string()],
                    },
                },
                OutputFormat::Human,
                "nodeup.toolchain.uninstall",
                json!({ "output": "human", "runtimes_count": 1 }),
            ),
            (
                Command::Toolchain {
                    command: ToolchainCommand::Link {
                        name: "linked".to_string(),
                        path: "/tmp/runtime".to_string(),
                    },
                },
                OutputFormat::Human,
                "nodeup.toolchain.link",
                json!({
                    "output": "human",
                    "name_provided": true,
                    "path_provided": true
                }),
            ),
            (
                Command::Default {
                    runtime: Some("lts".to_string()),
                },
                OutputFormat::Human,
                "nodeup.default",
                json!({ "output": "human", "runtime_provided": true }),
            ),
            (
                Command::Show {
                    command: ShowCommand::ActiveRuntime,
                },
                OutputFormat::Json,
                "nodeup.show.active-runtime",
                json!({ "output": "json", "show_command": "active-runtime" }),
            ),
            (
                Command::Show {
                    command: ShowCommand::Home,
                },
                OutputFormat::Human,
                "nodeup.show.home",
                json!({ "output": "human", "show_command": "home" }),
            ),
            (
                Command::Update {
                    runtimes: vec!["lts".to_string()],
                },
                OutputFormat::Json,
                "nodeup.update",
                json!({ "output": "json", "runtimes_count": 1 }),
            ),
            (
                Command::Check,
                OutputFormat::Human,
                "nodeup.check",
                json!({ "output": "human" }),
            ),
            (
                Command::Override {
                    command: OverrideCommand::List,
                },
                OutputFormat::Human,
                "nodeup.override.list",
                json!({ "output": "human" }),
            ),
            (
                Command::Override {
                    command: OverrideCommand::Set {
                        runtime: "lts".to_string(),
                        path: Some("/tmp/project".to_string()),
                    },
                },
                OutputFormat::Json,
                "nodeup.override.set",
                json!({ "output": "json", "path_provided": true }),
            ),
            (
                Command::Override {
                    command: OverrideCommand::Unset {
                        path: None,
                        nonexistent: true,
                    },
                },
                OutputFormat::Human,
                "nodeup.override.unset",
                json!({
                    "output": "human",
                    "path_provided": false,
                    "nonexistent": true
                }),
            ),
            (
                Command::Which {
                    runtime: Some("lts".to_string()),
                    command: "node".to_string(),
                },
                OutputFormat::Json,
                "nodeup.which",
                json!({
                    "output": "json",
                    "runtime_provided": true,
                    "command_provided": true
                }),
            ),
            (
                Command::Run {
                    install: true,
                    runtime: "lts".to_string(),
                    command: vec!["node".to_string(), "--version".to_string()],
                },
                OutputFormat::Human,
                "nodeup.run",
                json!({
                    "output": "human",
                    "install": true,
                    "runtime_provided": true,
                    "delegated_argv_len": 2
                }),
            ),
            (
                Command::SelfCmd {
                    command: SelfCommand::UpgradeData,
                },
                OutputFormat::Json,
                "nodeup.self.upgrade-data",
                json!({ "output": "json", "action": "upgrade-data" }),
            ),
            (
                Command::Completions {
                    shell: "zsh".to_string(),
                    command: Some("run".to_string()),
                },
                OutputFormat::Human,
                "nodeup.completions",
                json!({
                    "output": "human",
                    "shell": "zsh",
                    "command_scope_provided": true
                }),
            ),
        ];

        for (command, output, expected_path, expected_shape) in cases {
            let metadata = command_invocation_metadata(&command, output);
            assert_eq!(metadata.command_path, expected_path);
            assert_eq!(metadata.arg_shape, expected_shape);
        }
    }
}
