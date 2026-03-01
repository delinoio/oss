use std::ffi::OsString;

use clap::Parser;
use nodeup::{
    cli::Cli, commands, dispatch, errors::NodeupError, logging, types::ManagedAlias, NodeupApp,
};

fn main() {
    logging::init_logging();
    let json_error_output_requested = json_error_output_requested();

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

fn json_error_output_requested() -> bool {
    json_error_output_requested_from_args(std::env::args_os())
}

fn json_error_output_requested_from_args<I>(args: I) -> bool
where
    I: IntoIterator<Item = OsString>,
{
    let mut args = args.into_iter();
    let Some(argv0) = args.next() else {
        return false;
    };

    if ManagedAlias::from_argv0(argv0.as_os_str()).is_some() {
        return false;
    }

    let mut json_output_requested = false;

    while let Some(arg) = args.next() {
        let Some(arg) = arg.to_str() else {
            continue;
        };

        if arg == "--output" {
            if let Some(value) = args.next().and_then(|value| value.into_string().ok()) {
                match value.as_str() {
                    "json" => json_output_requested = true,
                    "human" => json_output_requested = false,
                    _ => {}
                }
            }
            continue;
        }

        if let Some(value) = arg.strip_prefix("--output=") {
            match value {
                "json" => json_output_requested = true,
                "human" => json_output_requested = false,
                _ => {}
            }
        }
    }

    json_output_requested
}

#[cfg(test)]
mod tests {
    use super::json_error_output_requested_from_args;

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
}
