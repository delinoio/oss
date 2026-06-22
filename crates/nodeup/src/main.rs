use std::ffi::OsString;

use clap::Parser;
use nodeup::{
    cli::{Cli, OutputColorMode},
    commands, dispatch,
    errors::{ErrorKind, NodeupError, NodeupErrorEnvelope},
    logging, output_style,
    types::ManagedAlias,
    NodeupApp,
};
use swc_malloc as _;

fn main() {
    let logging_context = logging_context();
    let output_preferences = management_output_preferences();
    let json_error_output_requested = output_preferences.json_error_output_requested;
    logging::init_logging(logging_context);

    match run() {
        Ok(code) => std::process::exit(code),
        Err(RunError::Nodeup(error)) => {
            if json_error_output_requested {
                let envelope = error.json_envelope();
                emit_json_error_envelope(&envelope, &error.message);
            } else {
                eprintln!(
                    "{}",
                    output_style::style_human_error(&error.message, output_preferences.color_mode)
                );
            }
            std::process::exit(error.exit_code());
        }
        Err(RunError::Clap(error)) => {
            if matches!(
                error.kind(),
                clap::error::ErrorKind::DisplayHelp | clap::error::ErrorKind::DisplayVersion
            ) {
                error.exit();
            }

            if json_error_output_requested {
                let envelope = clap_error_envelope(&error);
                emit_json_error_envelope(&envelope, &envelope.message);
                std::process::exit(envelope.exit_code);
            }

            error.exit();
        }
    }
}

fn run() -> Result<i32, RunError> {
    let app = NodeupApp::new()?;

    if let Some(exit_code) = dispatch::dispatch_managed_alias_if_needed(
        &app,
        management_output_preferences().json_error_output_requested,
    )? {
        return Ok(exit_code);
    }

    let cli = Cli::try_parse_from(normalized_management_args(std::env::args_os()))
        .map_err(RunError::Clap)?;
    commands::execute(cli, &app).map_err(RunError::Nodeup)
}

#[derive(Debug)]
enum RunError {
    Nodeup(NodeupError),
    Clap(clap::Error),
}

impl From<NodeupError> for RunError {
    fn from(value: NodeupError) -> Self {
        Self::Nodeup(value)
    }
}

fn clap_error_envelope(error: &clap::Error) -> NodeupErrorEnvelope {
    NodeupErrorEnvelope {
        kind: ErrorKind::InvalidInput,
        message: error.to_string().trim().to_string(),
        exit_code: error.exit_code(),
        diagnostics: None,
    }
}

fn emit_json_error_envelope(envelope: &NodeupErrorEnvelope, fallback_message: &str) {
    match serde_json::to_string(envelope) {
        Ok(payload) => eprintln!("{payload}"),
        Err(serialize_error) => eprintln!(
            "nodeup error: {} (failed to serialize JSON error payload: {})",
            fallback_message, serialize_error
        ),
    }
}

fn logging_context() -> logging::LoggingContext {
    logging_context_from_args(std::env::args_os())
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
struct ManagementOutputPreferences {
    json_error_output_requested: bool,
    color_mode: Option<OutputColorMode>,
}

fn management_output_preferences() -> ManagementOutputPreferences {
    management_output_preferences_from_args(std::env::args_os())
}

fn management_output_preferences_from_args<I>(args: I) -> ManagementOutputPreferences
where
    I: IntoIterator<Item = OsString>,
{
    let mut args = args.into_iter();
    let Some(argv0) = args.next() else {
        return ManagementOutputPreferences::default();
    };

    if ManagedAlias::from_argv0(argv0.as_os_str()).is_some() {
        return managed_alias_output_preferences_from_args(args);
    }

    management_output_preferences_from_management_args(args)
}

fn managed_alias_output_preferences_from_args<I>(args: I) -> ManagementOutputPreferences
where
    I: IntoIterator<Item = OsString>,
{
    let mut output_preferences = ManagementOutputPreferences::default();
    let mut output_value_expected = false;

    for arg in args {
        let Some(arg) = arg.to_str() else {
            output_value_expected = false;
            continue;
        };

        if output_value_expected {
            apply_output_value(arg, &mut output_preferences.json_error_output_requested);
            output_value_expected = false;
            continue;
        }

        if arg == "--" {
            break;
        }

        if arg == "--output" {
            output_value_expected = true;
            continue;
        }

        if let Some(value) = arg.strip_prefix("--output=") {
            apply_output_value(value, &mut output_preferences.json_error_output_requested);
            continue;
        }

        if arg.starts_with('-') {
            continue;
        }

        break;
    }

    output_preferences
}

fn normalized_management_args<I>(args: I) -> Vec<OsString>
where
    I: IntoIterator<Item = OsString>,
{
    let args = args.into_iter().collect::<Vec<_>>();
    let Some(command_index) = management_subcommand_index(&args) else {
        return args;
    };

    if args[command_index] == "completions" {
        return normalize_completion_global_flags(args, command_index);
    }

    args
}

fn management_subcommand_index(args: &[OsString]) -> Option<usize> {
    let mut output_value_expected = false;
    let mut color_value_expected = false;

    for (index, arg) in args.iter().enumerate().skip(1) {
        let Some(arg) = arg.to_str() else {
            output_value_expected = false;
            color_value_expected = false;
            continue;
        };

        if output_value_expected {
            output_value_expected = false;
            continue;
        }

        if color_value_expected {
            color_value_expected = false;
            continue;
        }

        if arg == "--output" {
            output_value_expected = true;
            continue;
        }

        if arg == "--color" {
            color_value_expected = true;
            continue;
        }

        if arg.starts_with('-') {
            continue;
        }

        return Some(index);
    }

    None
}

fn normalize_completion_global_flags(args: Vec<OsString>, command_index: usize) -> Vec<OsString> {
    let mut normalized = args[..=command_index].to_vec();
    let mut global_flags = Vec::new();
    let mut positional_args = Vec::new();
    let mut iter = args.into_iter().skip(command_index + 1).peekable();

    while let Some(arg) = iter.next() {
        let Some(raw_arg) = arg.to_str() else {
            positional_args.push(arg);
            continue;
        };

        if matches!(raw_arg, "--help" | "-h") {
            global_flags.push(arg);
            continue;
        }

        if raw_arg == "--" {
            positional_args.push(arg);
            positional_args.extend(iter);
            break;
        }

        if raw_arg == "--output" || raw_arg == "--color" {
            global_flags.push(arg);
            if let Some(value) = iter.next() {
                global_flags.push(value);
            }
            continue;
        }

        if raw_arg.starts_with("--output=") || raw_arg.starts_with("--color=") {
            global_flags.push(arg);
            continue;
        }

        positional_args.push(arg);
    }

    normalized.extend(global_flags);
    normalized.extend(positional_args);
    normalized
}

fn logging_context_from_args<I>(args: I) -> logging::LoggingContext
where
    I: IntoIterator<Item = OsString>,
{
    let mut args = args.into_iter();
    let Some(argv0) = args.next() else {
        return logging::LoggingContext::ManagementHuman;
    };

    if ManagedAlias::from_argv0(argv0.as_os_str()).is_some() {
        return if managed_alias_output_preferences_from_args(args).json_error_output_requested {
            logging::LoggingContext::ManagementJson
        } else {
            logging::LoggingContext::ManagedAlias
        };
    }

    if management_output_preferences_from_management_args(args).json_error_output_requested {
        logging::LoggingContext::ManagementJson
    } else {
        logging::LoggingContext::ManagementHuman
    }
}

#[cfg(test)]
fn json_error_output_requested_from_args<I>(args: I) -> bool
where
    I: IntoIterator<Item = OsString>,
{
    management_output_preferences_from_args(args).json_error_output_requested
}

#[cfg(test)]
fn color_mode_from_args<I>(args: I) -> Option<OutputColorMode>
where
    I: IntoIterator<Item = OsString>,
{
    management_output_preferences_from_args(args).color_mode
}

fn management_output_preferences_from_management_args<I>(args: I) -> ManagementOutputPreferences
where
    I: IntoIterator<Item = OsString>,
{
    let mut args = args.into_iter();
    let mut output_preferences = ManagementOutputPreferences::default();
    let mut command_scan_state = CommandScanState::BeforeSubcommand;
    let mut output_value_expected = false;
    let mut color_value_expected = false;

    loop {
        let Some(arg) = args.next() else {
            break;
        };

        if command_scan_state == CommandScanState::RunDelegated {
            break;
        }

        let Some(arg) = arg.to_str() else {
            output_value_expected = false;
            color_value_expected = false;
            continue;
        };

        if output_value_expected {
            apply_output_value(arg, &mut output_preferences.json_error_output_requested);
            output_value_expected = false;
            continue;
        }

        if color_value_expected {
            apply_color_value(arg, &mut output_preferences.color_mode);
            color_value_expected = false;
            continue;
        }

        if arg == "--output" {
            output_value_expected = true;
            continue;
        }

        if let Some(value) = arg.strip_prefix("--output=") {
            apply_output_value(value, &mut output_preferences.json_error_output_requested);
            continue;
        }

        if arg == "--color" {
            color_value_expected = true;
            continue;
        }

        if let Some(value) = arg.strip_prefix("--color=") {
            apply_color_value(value, &mut output_preferences.color_mode);
            continue;
        }

        match command_scan_state {
            CommandScanState::BeforeSubcommand => {
                if arg.starts_with('-') {
                    continue;
                }

                command_scan_state = if arg == "run" {
                    CommandScanState::RunBeforeRuntime
                } else {
                    CommandScanState::AfterSubcommand
                };
            }
            CommandScanState::RunBeforeRuntime => {
                // `run` captures all arguments after the runtime selector as delegated argv.
                // Stop scanning once runtime is encountered so delegated flags do not
                // affect nodeup's own output mode detection.
                if arg.starts_with('-') {
                    continue;
                }
                command_scan_state = CommandScanState::RunDelegated;
            }
            CommandScanState::RunDelegated | CommandScanState::AfterSubcommand => {}
        }
    }

    output_preferences
}

fn apply_output_value(value: &str, json_output_requested: &mut bool) {
    match value {
        "json" => *json_output_requested = true,
        "human" => *json_output_requested = false,
        _ => {}
    }
}

fn apply_color_value(value: &str, color_mode: &mut Option<OutputColorMode>) {
    if let Some(parsed_mode) = output_style::parse_output_color_mode(value) {
        *color_mode = Some(parsed_mode);
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum CommandScanState {
    BeforeSubcommand,
    RunBeforeRuntime,
    RunDelegated,
    AfterSubcommand,
}

#[cfg(test)]
mod tests {
    use nodeup::{cli::OutputColorMode, logging::LoggingContext};

    use super::{
        color_mode_from_args, json_error_output_requested_from_args, logging_context_from_args,
        normalized_management_args,
    };

    fn os_args(args: &[&str]) -> Vec<std::ffi::OsString> {
        args.iter()
            .map(std::ffi::OsString::from)
            .collect::<Vec<_>>()
    }

    #[test]
    fn json_output_flag_detects_split_form() {
        assert!(json_error_output_requested_from_args(os_args(&[
            "nodeup", "--output", "json", "show", "home",
        ])));
    }

    #[test]
    fn json_output_flag_detects_inline_form() {
        assert!(json_error_output_requested_from_args(os_args(&[
            "nodeup",
            "--output=json",
            "show",
            "home",
        ])));
    }

    #[test]
    fn managed_alias_invocation_detects_json_output_flags_before_delegated_args() {
        assert!(json_error_output_requested_from_args(os_args(&[
            "node", "--output", "json",
        ])));

        assert!(json_error_output_requested_from_args(os_args(&[
            "node",
            "--output=json",
        ])));
    }

    #[test]
    fn managed_alias_invocation_ignores_json_output_flags_after_delegated_args() {
        assert!(!json_error_output_requested_from_args(os_args(&[
            "node",
            "-e",
            "console.log(1)",
            "--output",
            "json",
        ])));
    }

    #[test]
    fn managed_alias_invocation_ignores_json_output_flags_after_delimiter() {
        assert!(!json_error_output_requested_from_args(os_args(&[
            "npm", "--", "--output", "json",
        ])));

        assert!(!json_error_output_requested_from_args(os_args(&[
            "npm",
            "--",
            "--output=json",
        ])));
    }

    #[test]
    fn managed_alias_invocation_selects_managed_alias_logging_context() {
        assert_eq!(
            logging_context_from_args(os_args(&["node"])),
            LoggingContext::ManagedAlias
        );
        assert_eq!(
            logging_context_from_args(os_args(&["yarn"])),
            LoggingContext::ManagedAlias
        );
        assert_eq!(
            logging_context_from_args(os_args(&["pnpm"])),
            LoggingContext::ManagedAlias
        );
    }

    #[test]
    fn managed_alias_json_output_selects_management_json_logging_context() {
        assert_eq!(
            logging_context_from_args(os_args(&["node", "--output", "json"])),
            LoggingContext::ManagementJson
        );
        assert_eq!(
            logging_context_from_args(os_args(&["npm", "--output=json"])),
            LoggingContext::ManagementJson
        );
    }

    #[test]
    fn managed_alias_delegated_json_output_keeps_managed_alias_logging_context() {
        assert_eq!(
            logging_context_from_args(os_args(&[
                "node",
                "-e",
                "console.log(1)",
                "--output",
                "json",
            ])),
            LoggingContext::ManagedAlias
        );
    }

    #[test]
    fn managed_alias_delimited_json_output_keeps_managed_alias_logging_context() {
        assert_eq!(
            logging_context_from_args(os_args(&["npm", "--", "--output", "json"])),
            LoggingContext::ManagedAlias
        );
    }

    #[test]
    fn run_delegated_output_flags_do_not_toggle_json_mode() {
        assert!(!json_error_output_requested_from_args(os_args(&[
            "nodeup", "run", "lts", "node", "--output", "json",
        ])));

        assert!(!json_error_output_requested_from_args(os_args(&[
            "nodeup",
            "run",
            "lts",
            "node",
            "--output=json",
        ])));
    }

    #[test]
    fn run_global_output_before_delegated_command_is_respected() {
        assert!(json_error_output_requested_from_args(os_args(&[
            "nodeup", "--output", "json", "run", "lts", "node", "--output", "human",
        ])));

        assert!(json_error_output_requested_from_args(os_args(&[
            "nodeup",
            "run",
            "--output=json",
            "lts",
            "node",
            "--output=human",
        ])));
    }

    #[test]
    fn positional_run_token_does_not_switch_run_mode_scanning() {
        assert!(json_error_output_requested_from_args(os_args(&[
            "nodeup",
            "which",
            "run",
            "--runtime",
            "22.1.0",
            "--output",
            "json",
        ])));
    }

    #[test]
    fn completions_global_flags_after_scope_are_normalized_before_positionals() {
        assert_eq!(
            normalized_management_args(os_args(&[
                "nodeup",
                "completions",
                "bash",
                "shim",
                "--output",
                "json",
                "--help",
            ])),
            os_args(&[
                "nodeup",
                "completions",
                "--output",
                "json",
                "--help",
                "bash",
                "shim",
            ])
        );
    }

    #[test]
    fn completions_unknown_option_like_scope_tokens_are_not_normalized() {
        assert_eq!(
            normalized_management_args(os_args(&[
                "nodeup",
                "--output",
                "json",
                "completions",
                "bash",
                "override",
                "set",
                "--path",
            ])),
            os_args(&[
                "nodeup",
                "--output",
                "json",
                "completions",
                "bash",
                "override",
                "set",
                "--path",
            ])
        );
    }

    #[test]
    fn completions_escaped_option_like_scope_tokens_are_not_normalized() {
        assert_eq!(
            normalized_management_args(os_args(&[
                "nodeup",
                "--output",
                "json",
                "completions",
                "bash",
                "--",
                "--help",
            ])),
            os_args(&[
                "nodeup",
                "--output",
                "json",
                "completions",
                "bash",
                "--",
                "--help",
            ])
        );
    }

    #[test]
    fn human_management_defaults_to_human_logging_context() {
        assert_eq!(
            logging_context_from_args(os_args(&["nodeup", "show", "home"])),
            LoggingContext::ManagementHuman
        );
    }

    #[test]
    fn color_output_flag_detects_split_form() {
        assert_eq!(
            color_mode_from_args(os_args(&["nodeup", "--color", "always", "show", "home"])),
            Some(OutputColorMode::Always)
        );
    }

    #[test]
    fn color_output_flag_detects_inline_form() {
        assert_eq!(
            color_mode_from_args(os_args(&["nodeup", "--color=never", "show", "home"])),
            Some(OutputColorMode::Never)
        );
    }

    #[test]
    fn managed_alias_invocation_ignores_color_flags() {
        assert_eq!(
            color_mode_from_args(os_args(&["node", "--color", "always"])),
            None
        );
    }

    #[test]
    fn run_delegated_color_flags_do_not_toggle_color_mode() {
        assert_eq!(
            color_mode_from_args(os_args(&[
                "nodeup", "run", "lts", "node", "--color", "always"
            ])),
            None
        );

        assert_eq!(
            color_mode_from_args(os_args(&["nodeup", "run", "lts", "node", "--color=always"])),
            None
        );
    }

    #[test]
    fn run_global_color_before_delegated_command_is_respected() {
        assert_eq!(
            color_mode_from_args(os_args(&[
                "nodeup", "--color", "always", "run", "lts", "node", "--color", "never",
            ])),
            Some(OutputColorMode::Always)
        );

        assert_eq!(
            color_mode_from_args(os_args(&[
                "nodeup",
                "run",
                "--color=never",
                "lts",
                "node",
                "--color=always",
            ])),
            Some(OutputColorMode::Never)
        );
    }
}
