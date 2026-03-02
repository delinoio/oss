use std::ffi::OsString;

use clap::Parser;
use nodeup::{
    cli::Cli, commands, dispatch, errors::NodeupError, logging, types::ManagedAlias, NodeupApp,
};

fn main() {
    let logging_context = logging_context();
    let json_error_output_requested = logging_context == logging::LoggingContext::ManagementJson;
    logging::init_logging(logging_context);

    match run() {
        Ok(code) => std::process::exit(code),
        Err(error) => {
            if json_error_output_requested {
                let envelope = error.json_envelope();
                match serde_json::to_string(&envelope) {
                    Ok(payload) => eprintln!("{payload}"),
                    Err(serialize_error) => eprintln!(
                        "nodeup error: {} (failed to serialize JSON error payload: {})",
                        error.message, serialize_error
                    ),
                }
            } else {
                eprintln!("nodeup error: {}", error.message);
            }
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

fn logging_context() -> logging::LoggingContext {
    logging_context_from_args(std::env::args_os())
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
        return logging::LoggingContext::ManagedAlias;
    }

    if json_error_output_requested_from_management_args(args) {
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
    logging_context_from_args(args) == logging::LoggingContext::ManagementJson
}

fn json_error_output_requested_from_management_args<I>(args: I) -> bool
where
    I: IntoIterator<Item = OsString>,
{
    let mut args = args.into_iter();
    let mut json_output_requested = false;
    let mut command_scan_state = CommandScanState::BeforeSubcommand;
    let mut output_value_expected = false;

    loop {
        let Some(arg) = args.next() else {
            break;
        };

        if command_scan_state == CommandScanState::RunDelegated {
            break;
        }

        let Some(arg) = arg.to_str() else {
            output_value_expected = false;
            continue;
        };

        if output_value_expected {
            apply_output_value(arg, &mut json_output_requested);
            output_value_expected = false;
            continue;
        }

        if arg == "--output" {
            output_value_expected = true;
            continue;
        }

        if let Some(value) = arg.strip_prefix("--output=") {
            apply_output_value(value, &mut json_output_requested);
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

    json_output_requested
}

fn apply_output_value(value: &str, json_output_requested: &mut bool) {
    match value {
        "json" => *json_output_requested = true,
        "human" => *json_output_requested = false,
        _ => {}
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
    use nodeup::logging::LoggingContext;

    use super::{json_error_output_requested_from_args, logging_context_from_args};

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
    fn managed_alias_invocation_ignores_json_output_flags() {
        assert!(!json_error_output_requested_from_args(os_args(&[
            "node", "--output", "json",
        ])));
    }

    #[test]
    fn managed_alias_invocation_selects_managed_alias_logging_context() {
        assert_eq!(
            logging_context_from_args(os_args(&["node"])),
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
    fn human_management_defaults_to_human_logging_context() {
        assert_eq!(
            logging_context_from_args(os_args(&["nodeup", "show", "home"])),
            LoggingContext::ManagementHuman
        );
    }
}
