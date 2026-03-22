use std::io::Write;

use clap::CommandFactory;
use clap_complete::{generate, Shell};
use tracing::info;

use crate::{
    cli::Cli,
    errors::{NodeupError, Result},
};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum CompletionShell {
    Bash,
    Zsh,
    Fish,
    PowerShell,
    Elvish,
}

impl CompletionShell {
    fn parse(raw: &str) -> Result<Self> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "bash" => Ok(Self::Bash),
            "zsh" => Ok(Self::Zsh),
            "fish" => Ok(Self::Fish),
            "powershell" => Ok(Self::PowerShell),
            "elvish" => Ok(Self::Elvish),
            _ => Err(NodeupError::invalid_input_with_hint(
                format!(
                    "Unsupported shell '{raw}'. Supported shells: bash, zsh, fish, powershell, \
                     elvish"
                ),
                "Use one of the supported shell values and retry `nodeup completions`.",
            )),
        }
    }

    fn as_str(self) -> &'static str {
        match self {
            Self::Bash => "bash",
            Self::Zsh => "zsh",
            Self::Fish => "fish",
            Self::PowerShell => "powershell",
            Self::Elvish => "elvish",
        }
    }

    fn to_clap_shell(self) -> Shell {
        match self {
            Self::Bash => Shell::Bash,
            Self::Zsh => Shell::Zsh,
            Self::Fish => Shell::Fish,
            Self::PowerShell => Shell::PowerShell,
            Self::Elvish => Shell::Elvish,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum CompletionScope {
    Toolchain,
    Default,
    Show,
    Update,
    Check,
    Override,
    Which,
    Run,
    SelfCmd,
    Completions,
}

impl CompletionScope {
    fn parse(raw: &str) -> Result<Self> {
        match raw.trim() {
            "toolchain" => Ok(Self::Toolchain),
            "default" => Ok(Self::Default),
            "show" => Ok(Self::Show),
            "update" => Ok(Self::Update),
            "check" => Ok(Self::Check),
            "override" => Ok(Self::Override),
            "which" => Ok(Self::Which),
            "run" => Ok(Self::Run),
            "self" => Ok(Self::SelfCmd),
            "completions" => Ok(Self::Completions),
            _ => Err(NodeupError::invalid_input_with_hint(
                format!(
                    "Unsupported command scope '{raw}'. Supported top-level commands: toolchain, \
                     default, show, update, check, override, which, run, self, completions"
                ),
                "Pass a valid top-level command name as the optional scope.",
            )),
        }
    }

    fn as_str(self) -> &'static str {
        match self {
            Self::Toolchain => "toolchain",
            Self::Default => "default",
            Self::Show => "show",
            Self::Update => "update",
            Self::Check => "check",
            Self::Override => "override",
            Self::Which => "which",
            Self::Run => "run",
            Self::SelfCmd => "self",
            Self::Completions => "completions",
        }
    }
}

pub fn completions(shell: &str, command: Option<&str>) -> Result<i32> {
    let scope_label = command.unwrap_or("<all-commands>");

    let parsed_shell = CompletionShell::parse(shell).inspect_err(|error| {
        log_generation_failure(shell, scope_label, "invalid-shell", error);
    })?;

    let parsed_scope = command
        .map(CompletionScope::parse)
        .transpose()
        .inspect_err(|error| {
            log_generation_failure(parsed_shell.as_str(), scope_label, "invalid-scope", error);
        })?;

    let script = generate_completion_script(parsed_shell, parsed_scope).inspect_err(|error| {
        log_generation_failure(
            parsed_shell.as_str(),
            scope_label,
            "generation-failed",
            error,
        );
    })?;

    let mut stdout = std::io::stdout();
    stdout.write_all(&script).map_err(|error| {
        let nodeup_error = NodeupError::internal_with_hint(
            format!("Failed to write completion script to stdout: {error}"),
            "Ensure stdout is writable and retry the command.",
        );
        log_generation_failure(
            parsed_shell.as_str(),
            scope_label,
            "stdout-write-failed",
            &nodeup_error,
        );
        nodeup_error
    })?;
    stdout.flush().map_err(|error| {
        let nodeup_error = NodeupError::internal_with_hint(
            format!("Failed to flush completion script output: {error}"),
            "Retry the command and ensure the output stream remains open.",
        );
        log_generation_failure(
            parsed_shell.as_str(),
            scope_label,
            "stdout-flush-failed",
            &nodeup_error,
        );
        nodeup_error
    })?;

    info!(
        command_path = "nodeup.completions",
        action = "generate",
        shell = parsed_shell.as_str(),
        scope = scope_label,
        scope_present = parsed_scope.is_some(),
        outcome = "generated",
        "Generated completion script"
    );

    Ok(0)
}

fn generate_completion_script(
    shell: CompletionShell,
    scope: Option<CompletionScope>,
) -> Result<Vec<u8>> {
    let mut root = Cli::command();
    if let Some(scope) = scope {
        apply_scope(&mut root, scope)?;
    }

    let mut buffer = Vec::new();
    generate(shell.to_clap_shell(), &mut root, "nodeup", &mut buffer);
    Ok(buffer)
}

fn apply_scope(root: &mut clap::Command, scope: CompletionScope) -> Result<()> {
    let selected = scope.as_str();
    if !root
        .get_subcommands()
        .any(|subcommand| subcommand.get_name() == selected)
    {
        return Err(NodeupError::invalid_input_with_hint(
            format!("Unsupported command scope '{selected}'"),
            "Choose a supported top-level command scope and retry.",
        ));
    }

    *root = root.clone().mut_subcommands(|subcommand| {
        if subcommand.get_name() == selected {
            subcommand
        } else {
            subcommand.hide(true)
        }
    });

    Ok(())
}

fn log_generation_failure(shell: &str, scope: &str, reason: &str, error: &NodeupError) {
    info!(
        command_path = "nodeup.completions",
        action = "generate",
        shell,
        scope,
        scope_present = scope != "<all-commands>",
        outcome = "failed",
        reason,
        error = %error.message,
        "Failed to generate completion script"
    );
}
