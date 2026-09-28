mod cli;

use std::io::{IsTerminal, Write};

use clap::{CommandFactory, Parser};
use cli::{Cli, Command};
use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt, Layer};

#[derive(Debug)]
enum Failure {
    Arguments,
}

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
    std::panic::set_hook(Box::new(|_| clibox_config::report_runtime_failure()));
    let raw: Vec<_> = std::env::args_os().collect();
    // Clap consumes this separator, but run env must distinguish it from an
    // assignment token and preserve the child command boundary.
    let leading_separator = raw.get(1).is_some_and(|s| s == "run")
        && raw.get(2).is_some_and(|s| s == "env")
        && raw.get(3).is_some_and(|s| s == "--");
    let cli = match Cli::try_parse_from(&raw) {
        Ok(cli) => cli,
        Err(error)
            if matches!(
                error.kind(),
                clap::error::ErrorKind::DisplayHelp
                    | clap::error::ErrorKind::DisplayHelpOnMissingArgumentOrSubcommand
                    | clap::error::ErrorKind::DisplayVersion
            ) =>
        {
            let code = error.exit_code();
            std::process::exit(if error.print().is_ok() { code } else { 1 });
        }
        Err(error) => {
            let message = cli::parser_message(error.kind(), &raw);
            if tracing::enabled!(tracing::Level::ERROR) {
                tracing::error!(
                    operation = "arguments",
                    classification = ?Failure::Arguments,
                    "error: arguments: {message}"
                );
            } else {
                // Invalid arguments still need actionable guidance when log
                // filtering disables tracing, without exposing clap's argv.
                let _ = writeln!(std::io::stderr(), "error: arguments: {message}");
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
    // Each command family owns its signal semantics. Wait-only CA environment
    // cleanup must never affect delegated children or offline processing.
    match command {
        Command::Configuration(command) => clibox_config::execute(command),
        Command::Wait(command) => std::process::exit(i32::from(clibox_wait::execute(command))),
        Command::System(command) => clibox_system::execute(command, leading_separator, &raw),
        Command::Transform(command) => {
            std::process::exit(i32::from(clibox_transform::execute(command)));
        }
        Command::Fspy(command) => std::process::exit(clibox_fspy::cli::execute(command)),
    }
}
