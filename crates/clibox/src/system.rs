use std::ffi::OsString;

use clap::Subcommand;

use crate::{clipboard, environment, error::Result, open, port, runtime};

#[derive(Subcommand)]
pub enum Action {
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
pub enum Run {
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

pub fn execute(command: Action, leading_separator: bool) -> Result<i32> {
    match command {
        Action::Run {
            command: Run::Env { mut args },
        } => {
            if leading_separator {
                args.insert(0, "--".into());
            }
            runtime::exit_child(environment::execute(args)?);
        }
        Action::Port { command } => return port::execute(command),
        Action::Open { target, app, wait } => open::execute(target, app, wait)?,
        Action::Clipboard { command } => clipboard::execute(command)?,
    }
    Ok(0)
}
