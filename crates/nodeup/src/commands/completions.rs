use clap::CommandFactory;
use clap_complete::Shell;
use serde::Serialize;
use tracing::info;

use crate::{
    cli::{Cli, CompletionShell, OutputFormat},
    errors::{NodeupError, Result},
};

#[derive(Debug, Serialize)]
#[serde(rename_all = "kebab-case")]
enum CompletionStatus {
    Generated,
}

#[derive(Debug, Serialize)]
struct CompletionResponse {
    shell: String,
    scope: Option<String>,
    status: CompletionStatus,
    script: String,
    script_bytes: usize,
}

pub fn generate(
    shell: CompletionShell,
    command_scope: Option<&str>,
    output: OutputFormat,
) -> Result<i32> {
    let shell_name = shell.as_str();
    let mut command = command_for_scope(command_scope)?;
    let script = render_completion_script(shell, &mut command)?;
    let scope = command_scope.map(str::to_string);
    let scope_label = scope.as_deref().unwrap_or("<all-commands>");

    info!(
        command_path = "nodeup.completions",
        action = "generate",
        shell = shell_name,
        scope = scope_label,
        scope_present = scope.is_some(),
        outcome = "generated",
        script_bytes = script.len(),
        "Generated completion script"
    );

    match output {
        OutputFormat::Human => print!("{script}"),
        OutputFormat::Json => {
            let response = CompletionResponse {
                shell: shell_name.to_string(),
                scope,
                status: CompletionStatus::Generated,
                script_bytes: script.len(),
                script,
            };
            println!("{}", serde_json::to_string_pretty(&response)?);
        }
    }

    Ok(0)
}

fn command_for_scope(scope: Option<&str>) -> Result<clap::Command> {
    let root = Cli::command();
    let Some(scope) = scope else {
        return Ok(root);
    };

    let normalized_scope = scope.trim();
    if normalized_scope.is_empty() {
        return Err(NodeupError::invalid_input(
            "Completion command scope cannot be empty",
        ));
    }
    if normalized_scope.split_whitespace().count() > 1 || normalized_scope.contains('.') {
        return Err(NodeupError::invalid_input(
            "Completion command scope must be a single top-level command",
        ));
    }

    let Some(scoped_subcommand) = root.find_subcommand(normalized_scope).cloned() else {
        let supported_scopes = root
            .get_subcommands()
            .map(|subcommand| subcommand.get_name().to_string())
            .collect::<Vec<_>>()
            .join(", ");
        return Err(NodeupError::invalid_input(format!(
            "Unknown completion command scope '{normalized_scope}'. Supported scopes: \
             {supported_scopes}"
        )));
    };

    let mut scoped_root = clap::Command::new("nodeup");
    for argument in root.get_arguments() {
        scoped_root = scoped_root.arg(argument.clone());
    }
    scoped_root = scoped_root.subcommand(scoped_subcommand);

    Ok(scoped_root)
}

fn render_completion_script(shell: CompletionShell, command: &mut clap::Command) -> Result<String> {
    let mut output = Vec::new();
    let bin_name = command.get_name().to_string();
    clap_complete::generate(clap_shell(shell), command, bin_name, &mut output);

    String::from_utf8(output).map_err(|error| {
        NodeupError::internal(format!("Completion script encoding failed: {error}"))
    })
}

fn clap_shell(shell: CompletionShell) -> Shell {
    match shell {
        CompletionShell::Bash => Shell::Bash,
        CompletionShell::Zsh => Shell::Zsh,
        CompletionShell::Fish => Shell::Fish,
    }
}
