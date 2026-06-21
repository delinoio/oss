use std::{collections::BTreeMap, io::Write};

use clap::CommandFactory;
use clap_complete::{generate, Shell};
use tracing::info;

use crate::{
    cli::Cli,
    errors::{ErrorKind, NodeupError, Result},
};

const SUPPORTED_SCOPE_LIST: &str =
    "toolchain, default, show, update, check, override, which, run, self, completions";

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
    fn parse(tokens: &[String], shell: CompletionShell) -> Result<Option<Self>> {
        let Some(first) = tokens.first() else {
            return Ok(None);
        };

        let parsed = match first.trim() {
            "toolchain" => Self::Toolchain,
            "default" => Self::Default,
            "show" => Self::Show,
            "update" => Self::Update,
            "check" => Self::Check,
            "override" => Self::Override,
            "which" => Self::Which,
            "run" => Self::Run,
            "self" => Self::SelfCmd,
            "completions" => Self::Completions,
            _ => return Err(unsupported_scope_error(tokens, shell, None)),
        };

        if tokens.len() > 1 {
            return Err(unsupported_scope_error(tokens, shell, Some(parsed)));
        }

        Ok(Some(parsed))
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

pub fn completions(shell: &str, command_scope_tokens: &[String]) -> Result<i32> {
    let scope_label = if command_scope_tokens.is_empty() {
        "<all-commands>".to_string()
    } else {
        command_scope_tokens.join(" ")
    };

    let parsed_shell = CompletionShell::parse(shell).inspect_err(|error| {
        log_generation_failure(shell, &scope_label, "invalid-shell", error);
    })?;

    let parsed_scope =
        CompletionScope::parse(command_scope_tokens, parsed_shell).inspect_err(|error| {
            log_generation_failure(parsed_shell.as_str(), &scope_label, "invalid-scope", error);
        })?;

    let script = generate_completion_script(parsed_shell, parsed_scope).inspect_err(|error| {
        log_generation_failure(
            parsed_shell.as_str(),
            &scope_label,
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
            &scope_label,
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
            &scope_label,
            "stdout-flush-failed",
            &nodeup_error,
        );
        nodeup_error
    })?;

    info!(
        command_path = "nodeup.completions",
        action = "generate",
        shell = parsed_shell.as_str(),
        scope = scope_label.as_str(),
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

fn unsupported_scope_error(
    tokens: &[String],
    shell: CompletionShell,
    suggested_scope: Option<CompletionScope>,
) -> NodeupError {
    let rejected_scope = tokens.join(" ");
    let mut diagnostics = BTreeMap::new();
    diagnostics.insert(
        "rejected_scope".to_string(),
        serde_json::Value::String(rejected_scope.clone()),
    );
    diagnostics.insert(
        "allowed_scope_category".to_string(),
        serde_json::Value::String("top-level-command".to_string()),
    );
    diagnostics.insert(
        "allowed_scopes".to_string(),
        serde_json::Value::Array(
            [
                "toolchain",
                "default",
                "show",
                "update",
                "check",
                "override",
                "which",
                "run",
                "self",
                "completions",
            ]
            .into_iter()
            .map(|scope| serde_json::Value::String(scope.to_string()))
            .collect(),
        ),
    );

    let hint = if let Some(scope) = suggested_scope {
        let suggested_scope = scope.as_str();
        diagnostics.insert(
            "suggested_scope".to_string(),
            serde_json::Value::String(suggested_scope.to_string()),
        );
        format!(
            "Only top-level command scopes are supported; use `nodeup completions {shell} \
             {suggested_scope}` instead.",
            shell = shell.as_str()
        )
    } else {
        "Pass one of the supported top-level command scopes: ".to_string()
            + SUPPORTED_SCOPE_LIST
            + "."
    };

    NodeupError::with_hint_and_diagnostics(
        ErrorKind::InvalidInput,
        format!(
            "Unsupported command scope '{rejected_scope}'. Supported top-level commands: \
             {SUPPORTED_SCOPE_LIST}"
        ),
        hint,
        diagnostics,
    )
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
