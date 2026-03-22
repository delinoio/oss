mod bump;
mod changed;
mod list;
mod publish;
mod targeting;

use std::io::IsTerminal;

use serde::Serialize;
use serde_json::{json, Value};
use tracing::info;

use crate::{
    cli::{BumpArgs, ChangedArgs, Cli, Command, PublishArgs, TargetArgs},
    errors::Result,
    types::{CargoMonoCommand, OutputColorMode, OutputFormat},
    CargoMonoApp,
};

const CARGO_MONO_OUTPUT_COLOR_ENV: &str = "CARGO_MONO_OUTPUT_COLOR";
const PUBLISH_PREFETCH_CONCURRENCY_ENV: &str = "CARGO_MONO_PUBLISH_PREFETCH_CONCURRENCY";
const NO_COLOR_ENV: &str = "NO_COLOR";
const CI_ENV: &str = "CI";
const GITHUB_ACTIONS_ENV: &str = "GITHUB_ACTIONS";
const TERM_ENV: &str = "TERM";
const ANSI_RESET: &str = "\x1b[0m";
const ANSI_SUCCESS: &str = "\x1b[32m";
const ANSI_WARNING: &str = "\x1b[33m";
const ANSI_ERROR: &str = "\x1b[31;1m";
const ANSI_ACCENT: &str = "\x1b[36m";
const ANSI_MUTED: &str = "\x1b[2m";

#[derive(Debug, Clone, Copy)]
pub struct OutputSettings {
    format: OutputFormat,
    color_enabled: bool,
}

impl OutputSettings {
    pub fn new(format: OutputFormat, color_arg: Option<OutputColorMode>) -> Self {
        let env_color_override = std::env::var(CARGO_MONO_OUTPUT_COLOR_ENV)
            .ok()
            .and_then(|value| OutputColorMode::parse_env(&value));
        let color_enabled = resolve_human_output_color_enabled(
            color_arg,
            env_color_override,
            std::env::var(NO_COLOR_ENV).ok(),
            std::io::stdout().is_terminal(),
            std::env::var(CI_ENV).ok(),
            std::env::var(GITHUB_ACTIONS_ENV).ok(),
            std::env::var(TERM_ENV).ok(),
        );

        Self {
            format,
            color_enabled,
        }
    }
}

pub fn execute(cli: Cli, app: &CargoMonoApp) -> Result<i32> {
    let output = OutputSettings::new(cli.output, cli.color);

    match cli.command {
        Command::List => list::execute(output, app),
        Command::Changed(args) => changed::execute(&args, output, app),
        Command::Bump(args) => bump::execute(&args, output, app),
        Command::Publish(args) => publish::execute(&args, output, app),
    }
}

pub fn log_invocation(command: &Command, output: OutputFormat, color: Option<OutputColorMode>) {
    log_command_invocation(command, output, color);
}

pub fn print_output<T: Serialize>(
    output: OutputSettings,
    human_line: &str,
    json_value: &T,
) -> Result<()> {
    match output.format {
        OutputFormat::Human => {
            if output.color_enabled {
                println!("{}", style_human_output(human_line));
            } else {
                println!("{human_line}");
            }
        }
        OutputFormat::Json => println!("{}", serde_json::to_string_pretty(json_value)?),
    }

    Ok(())
}

pub fn command_key(command: CargoMonoCommand) -> &'static str {
    command.as_str()
}

fn log_command_invocation(command: &Command, output: OutputFormat, color: Option<OutputColorMode>) {
    let metadata = command_invocation_metadata(command, output, color);
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
    color: Option<OutputColorMode>,
) -> CommandInvocationMetadata {
    match command {
        Command::List => CommandInvocationMetadata {
            command_path: command_key(CargoMonoCommand::List),
            arg_shape: json!({
                "output": output.as_str(),
                "color_arg": color.map(OutputColorMode::as_str).unwrap_or("unspecified")
            }),
        },
        Command::Changed(args) => CommandInvocationMetadata {
            command_path: command_key(CargoMonoCommand::Changed),
            arg_shape: changed_arg_shape(args, output, color),
        },
        Command::Bump(args) => CommandInvocationMetadata {
            command_path: command_key(CargoMonoCommand::Bump),
            arg_shape: bump_arg_shape(args, output, color),
        },
        Command::Publish(args) => CommandInvocationMetadata {
            command_path: command_key(CargoMonoCommand::Publish),
            arg_shape: publish_arg_shape(args, output, color),
        },
    }
}

fn changed_arg_shape(
    args: &ChangedArgs,
    output: OutputFormat,
    color: Option<OutputColorMode>,
) -> Value {
    json!({
        "output": output.as_str(),
        "color_arg": color.map(OutputColorMode::as_str).unwrap_or("unspecified"),
        "base_ref": args.base,
        "include_uncommitted": args.include_uncommitted,
        "direct_only": args.direct_only,
        "include_path": args.include_path,
        "exclude_path": args.exclude_path
    })
}

fn bump_arg_shape(args: &BumpArgs, output: OutputFormat, color: Option<OutputColorMode>) -> Value {
    json!({
        "output": output.as_str(),
        "color_arg": color.map(OutputColorMode::as_str).unwrap_or("unspecified"),
        "target_selector": target_selector_key(&args.target),
        "package_count": args.target.package.len(),
        "base_ref": args.changed.base,
        "include_uncommitted": args.changed.include_uncommitted,
        "direct_only": args.changed.direct_only,
        "include_path": args.changed.include_path,
        "exclude_path": args.changed.exclude_path,
        "level": args.level.as_str(),
        "preid_provided": args.preid.is_some(),
        "bump_dependents": args.bump_dependents,
        "allow_dirty": args.allow_dirty
    })
}

fn publish_arg_shape(
    args: &PublishArgs,
    output: OutputFormat,
    color: Option<OutputColorMode>,
) -> Value {
    json!({
        "output": output.as_str(),
        "color_arg": color.map(OutputColorMode::as_str).unwrap_or("unspecified"),
        "target_selector": target_selector_key(&args.target),
        "package_count": args.target.package.len(),
        "base_ref": args.changed.base,
        "include_uncommitted": args.changed.include_uncommitted,
        "direct_only": args.changed.direct_only,
        "include_path": args.changed.include_path,
        "exclude_path": args.changed.exclude_path,
        "dry_run": args.dry_run,
        "allow_dirty": args.allow_dirty,
        "registry_provided": args.registry.is_some(),
        "prefetch_registry_eligible": publish_prefetch_registry_eligible(args.registry.as_deref()),
        "prefetch_concurrency_env_set": std::env::var(PUBLISH_PREFETCH_CONCURRENCY_ENV).is_ok()
    })
}

fn publish_prefetch_registry_eligible(registry: Option<&str>) -> bool {
    registry.is_none_or(|value| value.eq_ignore_ascii_case("crates-io"))
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

fn resolve_human_output_color_enabled(
    color_arg: Option<OutputColorMode>,
    env_color_override: Option<OutputColorMode>,
    no_color: Option<String>,
    stdout_is_terminal: bool,
    ci: Option<String>,
    github_actions: Option<String>,
    term: Option<String>,
) -> bool {
    let selected_mode = color_arg
        .or(env_color_override)
        .unwrap_or(OutputColorMode::Auto);

    match selected_mode {
        OutputColorMode::Always => true,
        OutputColorMode::Never => false,
        OutputColorMode::Auto => {
            if color_arg.is_none() && env_color_override.is_none() && no_color.is_some() {
                return false;
            }

            stdout_is_terminal || auto_mode_ci_color_capable(ci, github_actions, term)
        }
    }
}

fn auto_mode_ci_color_capable(
    ci: Option<String>,
    github_actions: Option<String>,
    term: Option<String>,
) -> bool {
    if github_actions.is_some() {
        return true;
    }

    if ci.is_none() {
        return false;
    }

    !term
        .as_deref()
        .is_some_and(|value| value.eq_ignore_ascii_case("dumb"))
}

fn style_human_output(raw: &str) -> String {
    raw.lines()
        .map(style_human_output_line)
        .collect::<Vec<_>>()
        .join("\n")
}

fn style_human_output_line(line: &str) -> String {
    let Some(tone) = classify_human_line_tone(line) else {
        return line.to_string();
    };
    format!("{}{}{}", tone.as_ansi_prefix(), line, ANSI_RESET)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum HumanLineTone {
    Success,
    Warning,
    Error,
    Accent,
    Muted,
}

impl HumanLineTone {
    fn as_ansi_prefix(self) -> &'static str {
        match self {
            Self::Success => ANSI_SUCCESS,
            Self::Warning => ANSI_WARNING,
            Self::Error => ANSI_ERROR,
            Self::Accent => ANSI_ACCENT,
            Self::Muted => ANSI_MUTED,
        }
    }
}

fn classify_human_line_tone(line: &str) -> Option<HumanLineTone> {
    let trimmed = line.trim_start();
    if trimmed.is_empty() {
        return None;
    }

    if trimmed.starts_with("Publish summary:") {
        return Some(if publish_summary_has_failures(trimmed) {
            HumanLineTone::Error
        } else {
            HumanLineTone::Success
        });
    }

    if trimmed.starts_with("- failed ") || trimmed.starts_with("Summary:") {
        return Some(HumanLineTone::Error);
    }

    if trimmed.starts_with("- skipped ")
        || trimmed.contains("(non-publishable)")
        || trimmed.contains("(already-published)")
    {
        return Some(HumanLineTone::Warning);
    }

    if trimmed.starts_with("- published ")
        || trimmed.starts_with("- tagged ")
        || trimmed.contains("(publishable)")
        || trimmed.starts_with("Bumped ")
    {
        return Some(HumanLineTone::Success);
    }

    if trimmed.starts_with("Hint:") {
        return Some(HumanLineTone::Accent);
    }

    if trimmed.starts_with("Context:") {
        return Some(HumanLineTone::Muted);
    }

    if trimmed.starts_with("Workspace packages:")
        || trimmed.starts_with("Changed packages:")
        || trimmed.starts_with("No changed packages found")
        || trimmed.starts_with("No workspace packages found")
        || trimmed.starts_with("No publishable packages were selected")
        || trimmed.starts_with("No manifest changes were produced")
    {
        return Some(HumanLineTone::Accent);
    }

    None
}

fn publish_summary_has_failures(line: &str) -> bool {
    let Some(failed_segment) = line.split("failed=").nth(1) else {
        return false;
    };
    let failed_digits = failed_segment
        .chars()
        .take_while(|character| character.is_ascii_digit())
        .collect::<String>();
    let Ok(failed_count) = failed_digits.parse::<usize>() else {
        return false;
    };
    failed_count > 0
}

#[cfg(test)]
mod tests {
    use super::resolve_human_output_color_enabled;
    use crate::types::OutputColorMode;

    #[test]
    fn color_arg_has_highest_priority() {
        assert!(resolve_human_output_color_enabled(
            Some(OutputColorMode::Always),
            Some(OutputColorMode::Never),
            Some("1".to_string()),
            false,
            None,
            None,
            None,
        ));
        assert!(!resolve_human_output_color_enabled(
            Some(OutputColorMode::Never),
            Some(OutputColorMode::Always),
            None,
            true,
            None,
            None,
            None,
        ));
    }

    #[test]
    fn env_color_override_beats_no_color() {
        assert!(resolve_human_output_color_enabled(
            None,
            Some(OutputColorMode::Always),
            Some("1".to_string()),
            false,
            None,
            None,
            None,
        ));
    }

    #[test]
    fn no_color_disables_auto_without_override() {
        assert!(!resolve_human_output_color_enabled(
            None,
            None,
            Some("1".to_string()),
            true,
            None,
            None,
            None,
        ));
    }

    #[test]
    fn auto_uses_terminal_detection_by_default() {
        assert!(resolve_human_output_color_enabled(
            None, None, None, true, None, None, None,
        ));
        assert!(!resolve_human_output_color_enabled(
            None, None, None, false, None, None, None,
        ));
    }

    #[test]
    fn auto_enables_color_in_github_actions_logs() {
        assert!(resolve_human_output_color_enabled(
            None,
            None,
            None,
            false,
            Some("true".to_string()),
            Some("true".to_string()),
            Some("xterm-256color".to_string()),
        ));
    }

    #[test]
    fn auto_disables_color_for_ci_with_dumb_terminal() {
        assert!(!resolve_human_output_color_enabled(
            None,
            None,
            None,
            false,
            Some("true".to_string()),
            None,
            Some("dumb".to_string()),
        ));
    }
}
