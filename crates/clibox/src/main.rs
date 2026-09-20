mod cli;

use std::{io::IsTerminal, process::ExitCode};

use clap::{error::ErrorKind, CommandFactory, Parser};
use tracing_subscriber::{filter::filter_fn, prelude::*, EnvFilter};

fn main() -> ExitCode {
    let filter = EnvFilter::builder()
        .with_regex(false)
        .with_default_directive(tracing::level_filters::LevelFilter::WARN.into())
        .try_from_env()
        .unwrap_or_else(|_| EnvFilter::new("warn"));
    tracing_subscriber::registry()
        .with(filter)
        .with(
            tracing_subscriber::fmt::layer()
                .with_writer(std::io::stderr)
                .with_ansi(
                    std::io::stderr().is_terminal() && std::env::var_os("NO_COLOR").is_none(),
                )
                .without_time()
                .with_target(false)
                // Dependency debug logs are not part of our redacted diagnostic surface.
                .with_filter(filter_fn(|metadata| {
                    metadata.target().starts_with("clibox")
                })),
        )
        .init();
    std::panic::set_hook(Box::new(|_| {
        clibox_transform::report_runtime_failure();
    }));
    let raw: Vec<_> = std::env::args_os().collect();
    let leading_separator = raw.get(1).is_some_and(|s| s == "run")
        && raw.get(2).is_some_and(|s| s == "env")
        && raw.get(3).is_some_and(|s| s == "--");
    let cli = match cli::Cli::try_parse_from(raw) {
        Ok(cli) => cli,
        Err(error)
            if matches!(
                error.kind(),
                ErrorKind::DisplayHelp
                    | ErrorKind::DisplayHelpOnMissingArgumentOrSubcommand
                    | ErrorKind::DisplayVersion
            ) =>
        {
            return if error.print().is_ok() {
                ExitCode::from(error.exit_code() as u8)
            } else {
                ExitCode::from(1)
            };
        }
        Err(error) => {
            // Static parser diagnostics must remain visible even when logging is off.
            eprintln!("error: arguments: {}", cli::parser_message(error.kind()));
            return ExitCode::from(2);
        }
    };
    let Some(command) = cli.command else {
        return if cli::Cli::command().print_help().is_ok() {
            ExitCode::SUCCESS
        } else {
            ExitCode::from(1)
        };
    };
    // Each command family owns its signal semantics. Wait-only CA environment
    // cleanup must never affect delegated children or offline transformations.
    match command {
        cli::Command::Wait(command) => ExitCode::from(clibox_wait::execute(command)),
        cli::Command::System(command) => clibox_system::execute(command, leading_separator),
        cli::Command::Transform(command) => ExitCode::from(clibox_transform::execute(command)),
    }
}
