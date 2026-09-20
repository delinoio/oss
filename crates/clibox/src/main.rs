mod clipboard;
mod environment;
mod error;
mod open;
mod port;
mod runtime;

use std::{ffi::OsString, io::IsTerminal};

use clap::{CommandFactory, Parser, Subcommand};
use error::{Code, Failure, Result};

#[derive(Parser)]
#[command(
    name = "clibox",
    version,
    about = "Cross-platform developer utilities distributed through Cargo and npm"
)]
struct Cli {
    #[command(subcommand)]
    command: Option<Action>,
}

#[derive(Subcommand)]
enum Action {
    /// Run commands with a child-only environment.
    Run {
        #[command(subcommand)]
        command: Run,
    },
    /// Inspect or forcibly terminate local port owners.
    Port {
        #[command(subcommand)]
        command: port::Action,
    },
    /// Open one file, directory or registered URI.
    #[command(
        after_help = "Examples:\n  clibox open .\n  clibox open https://example.com\n  clibox \
                      open report.txt --app TextEdit --wait\n\n--wait observes application \
                      termination, not document or tab closure."
    )]
    Open {
        target: OsString,
        #[arg(long)]
        app: Option<OsString>,
        #[arg(long, requires = "app")]
        wait: bool,
    },
    /// Copy or paste the desktop session's ordinary text clipboard.
    Clipboard {
        #[command(subcommand)]
        command: clipboard::Action,
    },
}

#[derive(Subcommand)]
enum Run {
    /// Set cross-env compatible assignments and wait for a child command.
    #[command(
        after_help = "Examples:\n  clibox run env NODE_ENV=production node build.js\n  clibox run \
                      env -- node script.js\n\nInherits cwd and stdio. No shell expressions. \
                      Empty child arguments and exit signals are preserved."
    )]
    Env {
        #[arg(value_name = "KEY=VALUE ... COMMAND ARG", trailing_var_arg = true, allow_hyphen_values = true, num_args = 1..)]
        args: Vec<OsString>,
    },
}

fn execute(cli: Cli, leading_separator: bool) -> Result<i32> {
    match cli.command {
        None => {
            Cli::command().print_help().map_err(|e| Failure::io(&e))?;
            println!();
        }
        Some(Action::Run {
            command: Run::Env { mut args },
        }) => {
            if leading_separator {
                args.insert(0, "--".into());
            }
            runtime::exit_child(environment::execute(args)?);
        }
        Some(Action::Port { command }) => return port::execute(command),
        Some(Action::Open { target, app, wait }) => open::execute(target, app, wait)?,
        Some(Action::Clipboard { command }) => clipboard::execute(command)?,
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
        Err(_) => {
            // Clap's default diagnostics echo rejected values, which can contain secrets.
            eprintln!(
                "error: invalid command arguments; use clibox --help or the command's --help."
            );
            std::process::exit(2);
        }
    };
    let filter =
        tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "warn".into());
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_writer(std::io::stderr)
        .with_ansi(std::io::stderr().is_terminal() && std::env::var_os("NO_COLOR").is_none())
        .without_time()
        .with_target(false)
        .init();
    let result = runtime::install_signals().and_then(|()| execute(cli, leading_separator));
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
