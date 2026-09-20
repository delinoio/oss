mod base64;
mod cli;
mod clipboard;
mod environment;
mod error;
mod hash;
mod io;
mod open;
mod port;
mod publication;
mod runtime;
mod system;
mod text;
mod time;
mod transform;
mod transform_error;

use std::{io::IsTerminal, process::ExitCode};

use clap::{error::ErrorKind, CommandFactory, Parser};
use tracing_subscriber::{filter::filter_fn, prelude::*, EnvFilter};

fn main() -> ExitCode {
    let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("warn"));
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
        transform_error::report(transform_error::Error::runtime(
            transform_error::Code::Runtime,
        ));
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
                ErrorKind::DisplayHelp | ErrorKind::DisplayVersion
            ) =>
        {
            return if error.print().is_ok() {
                ExitCode::SUCCESS
            } else {
                ExitCode::from(1)
            };
        }
        Err(_) => {
            transform_error::report(transform_error::Error::argument(
                transform_error::Code::Arguments,
            ));
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
    let command = match command {
        cli::Command::System(command) => {
            let result = runtime::install_signals()
                .and_then(|()| system::execute(command, leading_separator));
            let status = match result {
                Ok(status) => status,
                Err(error) => {
                    error.report("clibox");
                    if error.code == error::Code::InvalidInput {
                        2
                    } else {
                        1
                    }
                }
            };
            runtime::finish(status);
        }
        cli::Command::Transform(command) => command,
    };
    match transform::execute(command) {
        Ok(status) => ExitCode::from(status),
        Err(error) => {
            transform_error::report(error);
            ExitCode::from(error.exit)
        }
    }
}
