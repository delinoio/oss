use std::ffi::OsString;

use clap::Subcommand;

use crate::{clipboard, cpus, environment, error::Result, open, port, run, runtime};

#[derive(Subcommand)]
pub enum Action {
    /// Run a child environment or a workload with local execution controls.
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
    /// Query local CPU counts for portable scripts and builds.
    System {
        #[command(subcommand)]
        command: cpus::Action,
    },
}

#[derive(Subcommand)]
pub enum Run {
    /// Set cross-env compatible assignments and wait for a child command.
    #[command(
        after_help = "Examples:\n  clibox run env NODE_ENV=production node build.js\n  clibox run \
                      env -- node script.js\n\nInherits cwd and stdio. No shell expressions. \
                      Empty child arguments and exit signals are preserved.\n\nChild exit status \
                      and supported termination signals are preserved. Invalid arguments exit 2; \
                      startup failures exit 1."
    )]
    Env {
        /// Child-only assignments followed by the command and its literal
        /// arguments.
        #[arg(value_name = "KEY=VALUE ... COMMAND ARG", trailing_var_arg = true, allow_hyphen_values = true, num_args = 1..)]
        args: Vec<OsString>,
    },
    /// Admit one workload through a shared local token bucket.
    #[command(
        name = "with-rate-limit",
        after_help = "Example: clibox run with-rate-limit --name publish --limit 2 --period 1m -- \
                      npm publish\n\nThe limit admits executions, not concurrent descendants. \
                      Names and project paths are hashed before local state is stored. Exit \
                      codes: 124 admission timeout, 2 invalid arguments, 1 wrapper failure; \
                      natural child status is preserved."
    )]
    WithRateLimit(run::RateLimit),
    /// Run one workload while holding a shared local exclusive lock.
    #[command(
        name = "with-lock",
        after_help = "Example: clibox run with-lock --name database-migrate -- pnpm \
                      migrate\n\nSame-key nested locks contend normally and can wait \
                      indefinitely. Exit codes: 75 locked/fail, 0 locked/skip, 124 wait timeout, \
                      2 invalid arguments, 1 wrapper failure; natural child status is preserved."
    )]
    WithLock(run::Lock),
    /// Wait for HTTP readiness before running a workload, optionally owning a
    /// service.
    #[command(
        name = "with-service",
        after_help = "Examples:\n  clibox run with-service http://127.0.0.1:3000/health -- npm test\n  clibox run with-service http://127.0.0.1:3000/health --service node server.js -- npm test\n\nA managed service is started only after a retryable not-ready preflight. The first standalone -- after --service separates service arguments from the workload; -- inside service arguments is unsupported. External services are never terminated. Exit codes: 124 readiness timeout, 2 invalid arguments, 1 wrapper failure; natural child status is preserved."
    )]
    WithService(run::Service),
    /// Retry a workload after eligible nonzero numeric exit statuses.
    #[command(
        name = "with-retry",
        after_help = "Example: clibox run with-retry --max-attempts 5 --jitter none -- cargo \
                      fetch\n\nAttempts share literal argv, prepared environment, cwd, and stdin; \
                      consumed stdin is not replayed. Spawn failures and Unix signal termination \
                      are not retried. Exit codes: 124 overall timeout, 2 invalid arguments, 1 \
                      wrapper failure; the final child status is preserved."
    )]
    WithRetry(run::Retry),
    /// Bound a workload's total runtime and optional output-idle interval.
    #[command(
        name = "with-timeout",
        after_help = "Example: clibox run with-timeout --timeout 10m --idle-timeout 30s -- npm \
                      test\n\nAny stdout or stderr bytes reset the idle timer. Output is \
                      forwarded immediately, but workload TTY identity is not guaranteed. A \
                      timeout exits 124 after bounded cleanup."
    )]
    WithTimeout(run::Timeout),
}

impl Action {
    pub fn operation(&self) -> &'static str {
        match self {
            Self::Run {
                command: Run::Env { .. },
            } => "run-env",
            Self::Run {
                command: Run::WithRateLimit(_),
            } => "run-with-rate-limit",
            Self::Run {
                command: Run::WithLock(_),
            } => "run-with-lock",
            Self::Run {
                command: Run::WithService(_),
            } => "run-with-service",
            Self::Run {
                command: Run::WithRetry(_),
            } => "run-with-retry",
            Self::Run {
                command: Run::WithTimeout(_),
            } => "run-with-timeout",
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
            Self::System {
                command: cpus::Action::Cpus { .. },
            } => "system-cpus",
        }
    }
}

fn finish_wrapper_outcome(outcome: run::Outcome) -> Result<i32> {
    match outcome {
        run::Outcome::Child(status) => {
            runtime::check_cancelled()?;
            runtime::exit_child(status);
        }
        run::Outcome::Code(code) => Ok(code),
    }
}

pub fn execute(command: Action, leading_separator: bool, raw: &[OsString]) -> Result<i32> {
    match command {
        Action::Run {
            command: Run::Env { mut args },
        } => {
            if leading_separator {
                args.insert(0, "--".into());
            }
            runtime::exit_child(environment::execute(args)?);
        }
        Action::Run {
            command: Run::WithRateLimit(options),
        } => return finish_wrapper_outcome(run::execute(run::Command::RateLimit(options), raw)?),
        Action::Run {
            command: Run::WithLock(options),
        } => return finish_wrapper_outcome(run::execute(run::Command::Lock(options), raw)?),
        Action::Run {
            command: Run::WithService(options),
        } => return finish_wrapper_outcome(run::execute(run::Command::Service(options), raw)?),
        Action::Run {
            command: Run::WithRetry(options),
        } => return finish_wrapper_outcome(run::execute(run::Command::Retry(options), raw)?),
        Action::Run {
            command: Run::WithTimeout(options),
        } => return finish_wrapper_outcome(run::execute(run::Command::Timeout(options), raw)?),
        Action::Port { command } => return port::execute(command),
        Action::Open { target, app, wait } => open::execute(target, app, wait)?,
        Action::Clipboard { command } => clipboard::execute(command)?,
        Action::System { command } => cpus::execute(command)?,
    }
    Ok(0)
}
