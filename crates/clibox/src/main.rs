mod base64;
mod cli;
mod error;
mod hash;
mod io;
mod publication;
mod runtime;
mod text;
mod time;

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
        error::report(error::Error::runtime(error::Code::Runtime));
    }));
    let cli = match cli::Cli::try_parse() {
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
            error::report(error::Error::argument(error::Code::Arguments));
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
    match runtime::execute(command) {
        Ok(status) => ExitCode::from(status),
        Err(error) => {
            error::report(error);
            ExitCode::from(error.exit)
        }
    }
}
