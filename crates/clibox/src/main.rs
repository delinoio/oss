mod cli;
mod clipboard;
mod environment;
mod error;
mod open;
mod port;
mod probe;
mod runtime;
mod wait;
mod wait_command;

use std::io::{self, IsTerminal};

use clap::{CommandFactory, Parser};
use cli::{Cli, Command, Run, Utility};
use error::{Code, Failure, Result};
use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt, Layer};

fn execute(command: Option<Utility>, leading_separator: bool) -> Result<i32> {
    match command {
        None => {
            Cli::command().print_help().map_err(|e| Failure::io(&e))?;
            println!();
        }
        Some(Utility::Run {
            command: Run::Env { mut args },
        }) => {
            if leading_separator {
                args.insert(0, "--".into());
            }
            runtime::exit_child(environment::execute(args)?);
        }
        Some(Utility::Port { command }) => return port::execute(command),
        Some(Utility::Open { target, app, wait }) => open::execute(target, app, wait)?,
        Some(Utility::Clipboard { command }) => clipboard::execute(command)?,
    }
    Ok(0)
}

fn main() {
    let raw: Vec<_> = std::env::args_os().collect();
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
            error.exit()
        }
        Err(error) => {
            eprintln!("error: {}", cli::parser_message(error.kind()));
            std::process::exit(2);
        }
    };
    let filter = tracing_subscriber::EnvFilter::builder()
        .with_regex(false)
        .with_default_directive(tracing::level_filters::LevelFilter::WARN.into())
        .try_from_env()
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("warn"));
    tracing_subscriber::registry()
        .with(
            tracing_subscriber::fmt::layer()
                .with_writer(io::stderr)
                .with_ansi(io::stderr().is_terminal() && std::env::var_os("NO_COLOR").is_none())
                // Dependency diagnostics can contain URLs, DNS names, or TLS details.
                // Never enable them, even with RUST_LOG=trace or dependency directives.
                .with_filter(tracing_subscriber::filter::filter_fn(|meta| {
                    meta.target().starts_with("clibox")
                }))
                .with_filter(filter),
        )
        .init();

    // Readiness waits return numeric cancellation results with final JSON;
    // utility commands retain delegated signal semantics and child environments.
    // Never install both signal handlers or clear CA variables for run env.
    let command = match cli.command {
        Some(Command::Wait(command)) => {
            std::process::exit(i32::from(wait_command::execute(command)))
        }
        Some(Command::Utility(command)) => Some(command),
        None => None,
    };
    let result = runtime::install_signals().and_then(|()| execute(command, leading_separator));
    let code = match result {
        Ok(code) => code,
        Err(error) => {
            error.report("clibox");
            if error.code == Code::InvalidInput {
                2
            } else {
                1
            }
        }
    };
    runtime::finish(code);
}
