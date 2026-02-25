mod bump;
mod changed;
mod list;
mod publish;

use serde::Serialize;
use serde_json::{json, Value};
use tracing::info;

use crate::{
    cli::{BumpArgs, ChangedArgs, Cli, Command, PublishArgs, TargetArgs},
    errors::Result,
    types::{CargoMonoCommand, OutputFormat},
    CargoMonoApp,
};

pub fn execute(cli: Cli, app: &CargoMonoApp) -> Result<i32> {
    log_command_invocation(&cli.command, cli.output);

    match cli.command {
        Command::List => list::execute(cli.output, app),
        Command::Changed(args) => changed::execute(&args, cli.output, app),
        Command::Bump(args) => bump::execute(&args, cli.output, app),
        Command::Publish(args) => publish::execute(&args, cli.output, app),
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

pub fn command_key(command: CargoMonoCommand) -> &'static str {
    command.as_str()
}

fn log_command_invocation(command: &Command, output: OutputFormat) {
    let metadata = command_invocation_metadata(command, output);
    let arg_shape = serde_json::to_string(&metadata.arg_shape).unwrap_or_else(|_| "{}".to_string());

    info!(
        command_path = metadata.command_path,
        arg_shape = %arg_shape,
        action = "invoke-command",
        outcome = "started",
        "Running command"
    );
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct CommandInvocationMetadata {
    command_path: &'static str,
    arg_shape: Value,
}

fn command_invocation_metadata(
    command: &Command,
    output: OutputFormat,
) -> CommandInvocationMetadata {
    match command {
        Command::List => CommandInvocationMetadata {
            command_path: command_key(CargoMonoCommand::List),
            arg_shape: json!({ "output": output.as_str() }),
        },
        Command::Changed(args) => CommandInvocationMetadata {
            command_path: command_key(CargoMonoCommand::Changed),
            arg_shape: changed_arg_shape(args, output),
        },
        Command::Bump(args) => CommandInvocationMetadata {
            command_path: command_key(CargoMonoCommand::Bump),
            arg_shape: bump_arg_shape(args, output),
        },
        Command::Publish(args) => CommandInvocationMetadata {
            command_path: command_key(CargoMonoCommand::Publish),
            arg_shape: publish_arg_shape(args, output),
        },
    }
}

fn changed_arg_shape(args: &ChangedArgs, output: OutputFormat) -> Value {
    json!({
        "output": output.as_str(),
        "base_ref": args.base,
        "include_uncommitted": args.include_uncommitted,
        "direct_only": args.direct_only
    })
}

fn bump_arg_shape(args: &BumpArgs, output: OutputFormat) -> Value {
    json!({
        "output": output.as_str(),
        "target_selector": target_selector_key(&args.target),
        "package_count": args.target.package.len(),
        "base_ref": args.changed.base,
        "include_uncommitted": args.changed.include_uncommitted,
        "direct_only": args.changed.direct_only,
        "level": args.level.as_str(),
        "preid_provided": args.preid.is_some(),
        "bump_dependents": args.bump_dependents,
        "allow_dirty": args.allow_dirty
    })
}

fn publish_arg_shape(args: &PublishArgs, output: OutputFormat) -> Value {
    json!({
        "output": output.as_str(),
        "target_selector": target_selector_key(&args.target),
        "package_count": args.target.package.len(),
        "base_ref": args.changed.base,
        "include_uncommitted": args.changed.include_uncommitted,
        "direct_only": args.changed.direct_only,
        "dry_run": args.dry_run,
        "allow_dirty": args.allow_dirty,
        "registry_provided": args.registry.is_some()
    })
}

fn target_selector_key(target: &TargetArgs) -> &'static str {
    if target.changed {
        return "changed";
    }

    if !target.package.is_empty() {
        return "package";
    }

    "all"
}
