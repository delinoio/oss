mod cli;
mod clipboard;
mod config_command;
mod config_runtime;
mod dotenv;
mod environment;
mod error;
mod open;
mod port;
mod probe;
mod publication;
mod runtime;
mod wait;
mod wait_command;
mod yaml;

use std::io::{IsTerminal, Write};

use clap::{CommandFactory, Parser};
use cli::{Cli, Command, Run, Utility};
use config_runtime::{Error, Failure};
use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt, Layer};

fn main() {
    // Filter expressions and dependency diagnostics can contain user input.
    let filter = tracing_subscriber::EnvFilter::builder()
        .with_regex(false)
        .with_default_directive(tracing::level_filters::LevelFilter::WARN.into())
        .try_from_env()
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("warn"));
    tracing_subscriber::registry()
        .with(
            tracing_subscriber::fmt::layer()
                // The fallback uses eprintln!, which panics on closed stderr
                // and can recurse through the redacted panic hook.
                .log_internal_errors(false)
                .with_writer(std::io::stderr)
                .with_ansi(
                    std::io::stderr().is_terminal() && std::env::var_os("NO_COLOR").is_none(),
                )
                .without_time()
                .with_target(false)
                // Never expose dependency URLs, DNS names, or TLS details,
                // even with RUST_LOG=trace or explicit dependency directives.
                .with_filter(tracing_subscriber::filter::filter_fn(|meta| {
                    meta.target().starts_with("clibox")
                }))
                .with_filter(filter),
        )
        .init();
    // Dependency panics can contain input slices; replace the panic payload and
    // location with a stable classification. Normal errors never panic.
    std::panic::set_hook(Box::new(|_| {
        Error::from(Failure::Internal).report("runtime")
    }));
    let raw: Vec<_> = std::env::args_os().collect();
    // Clap consumes this separator, but run env must distinguish it from an
    // assignment token and preserve the child command boundary.
    let leading_separator = raw.get(1).is_some_and(|s| s == "run")
        && raw.get(2).is_some_and(|s| s == "env")
        && raw.get(3).is_some_and(|s| s == "--");
    let cli = match Cli::try_parse_from(raw) {
        Ok(cli) => cli,
        Err(error)
            if matches!(
                error.kind(),
                clap::error::ErrorKind::DisplayHelp | clap::error::ErrorKind::DisplayVersion
            ) =>
        {
            std::process::exit(if error.print().is_ok() { 0 } else { 1 });
        }
        Err(error) => {
            let message = cli::parser_message(error.kind());
            if tracing::enabled!(tracing::Level::ERROR) {
                tracing::error!(
                    operation = "arguments",
                    classification = ?Failure::Arguments,
                    "error: {message}"
                );
            } else {
                // Invalid arguments still need actionable guidance when log
                // filtering disables tracing, without exposing clap's argv.
                let _ = writeln!(std::io::stderr(), "error: {message}");
            }
            std::process::exit(2);
        }
    };
    let Some(command) = cli.command else {
        let code = if Cli::command().print_help().is_ok() {
            if std::io::Write::write_all(&mut std::io::stdout(), b"\n").is_ok() {
                0
            } else {
                1
            }
        } else {
            1
        };
        std::process::exit(code);
    };
    match command {
        Command::Configuration(command) => config_command::execute(command),
        Command::Wait(command) => std::process::exit(i32::from(wait_command::execute(command))),
        Command::Utility(command) => match command {
            Utility::Run {
                command: Run::Env { mut args },
            } => execute_utility(|| {
                if leading_separator {
                    args.insert(0, "--".into());
                }
                runtime::exit_child(environment::execute(args)?);
            }),
            Utility::Port { command } => execute_utility(|| port::execute(command)),
            Utility::Open { target, app, wait } => execute_utility(|| {
                open::execute(target, app, wait)?;
                Ok(0)
            }),
            Utility::Clipboard { command } => execute_utility(|| {
                clipboard::execute(command)?;
                Ok(0)
            }),
        },
    }
}

fn execute_utility(work: impl FnOnce() -> error::Result<i32>) -> ! {
    let result = runtime::install_signals().and_then(|()| work());
    let code = match result {
        Ok(code) => code,
        Err(error) => {
            error.report("clibox");
            if error.code == error::Code::InvalidInput {
                2
            } else {
                1
            }
        }
    };
    runtime::finish(code);
}
