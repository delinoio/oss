use std::ffi::OsString;

use clap::Subcommand;

use crate::{clipboard, environment, error::Result, open, port, runtime};

#[derive(Subcommand)]
pub enum Action {
    /// Run commands with a child-only environment.
    Env {
        #[command(subcommand)]
        command: Env,
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
                      termination, not document or tab closure.\n\nExit codes: 0 success, 1 \
                      operation failure, 2 invalid arguments, 130 Ctrl+C/Windows Ctrl+Break, 143 \
                      Unix SIGTERM."
    )]
    Open {
        /// One file, directory, or registered URI.
        target: OsString,
        /// Application name/path on macOS, or executable path/PATH name
        /// elsewhere.
        #[arg(long)]
        app: Option<OsString>,
        /// Wait for the explicit application to exit; requires --app.
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
pub enum Env {
    /// Set cross-env compatible assignments and wait for a child command.
    #[command(
        after_help = "Examples:\n  clibox env run NODE_ENV=production node build.js\n  clibox env \
                      run -- node script.js\n\nInherits cwd and stdio. No shell expressions. \
                      Empty child arguments and exit signals are preserved.\n\nChild exit status \
                      and supported termination signals are preserved. Invalid arguments exit 2; \
                      startup failures exit 1."
    )]
    Run {
        /// Child-only assignments followed by the command and its literal
        /// arguments.
        #[arg(value_name = "KEY=VALUE ... COMMAND ARG", trailing_var_arg = true, allow_hyphen_values = true, num_args = 1..)]
        args: Vec<OsString>,
    },
}

impl Action {
    pub fn operation(&self) -> &'static str {
        match self {
            Self::Env { .. } => "env-run",
            Self::Port {
                command: port::Action::List { .. },
            } => "port-list",
            Self::Port {
                command: port::Action::Kill { .. },
            } => "port-kill",
            Self::Open { .. } => "open",
            Self::Clipboard {
                command: clipboard::Action::Copy { .. },
            } => "clipboard-copy",
            Self::Clipboard {
                command: clipboard::Action::Paste,
            } => "clipboard-paste",
        }
    }
}

pub fn execute(command: Action, leading_separator: bool) -> Result<i32> {
    match command {
        Action::Env {
            command: Env::Run { mut args },
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
