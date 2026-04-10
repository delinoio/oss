use std::ffi::OsString;

use clap::{Args, CommandFactory, FromArgMatches, Parser, Subcommand};

use crate::{
    analysis::render_after_long_help,
    error::{Result, WithWatchError},
    snapshot::ChangeDetectionMode,
};

#[derive(Debug, Parser)]
#[command(
    name = "with-watch",
    version,
    about = "Run commands again when their inputs change"
)]
pub struct Cli {
    /// Disable content hashing and compare only file metadata.
    #[arg(long, global = true)]
    pub no_hash: bool,

    /// Run a quoted shell command line that may contain `&&`, `||`, or `|`.
    #[arg(long, global = true, value_name = "EXPR")]
    pub shell: Option<String>,

    #[command(subcommand)]
    pub command: Option<Command>,
}

#[derive(Debug, Subcommand)]
pub enum Command {
    /// Run an arbitrary command with explicit watched inputs.
    Exec(ExecArgs),
    #[command(external_subcommand)]
    Passthrough(Vec<OsString>),
}

#[derive(Debug, Clone, Args)]
pub struct ExecArgs {
    /// Watched filesystem inputs expressed as repeatable glob or path values.
    #[arg(long = "input", value_name = "GLOB", required = true)]
    pub input: Vec<String>,

    /// Command to execute after `--`.
    #[arg(required = true, trailing_var_arg = true, allow_hyphen_values = true)]
    pub command: Vec<OsString>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CommandMode {
    Passthrough {
        argv: Vec<OsString>,
    },
    Shell {
        expression: String,
    },
    Exec {
        inputs: Vec<String>,
        argv: Vec<OsString>,
    },
}

impl Cli {
    pub fn command_with_inventory() -> clap::Command {
        <Self as CommandFactory>::command().after_long_help(render_after_long_help())
    }

    pub fn parse_with_inventory() -> Self {
        let matches = Self::command_with_inventory().get_matches();
        Self::from_arg_matches(&matches).unwrap_or_else(|error| error.exit())
    }

    pub fn change_detection_mode(&self) -> ChangeDetectionMode {
        if self.no_hash {
            ChangeDetectionMode::MtimeOnly
        } else {
            ChangeDetectionMode::ContentHash
        }
    }

    pub fn command_mode(&self) -> Result<CommandMode> {
        match (&self.shell, &self.command) {
            (Some(_), Some(_)) => Err(WithWatchError::ConflictingModes),
            (Some(expression), None) => {
                let trimmed = expression.trim();
                if trimmed.is_empty() {
                    return Err(WithWatchError::EmptyShellExpression);
                }
                Ok(CommandMode::Shell {
                    expression: trimmed.to_string(),
                })
            }
            (None, Some(Command::Exec(exec))) => {
                if exec.command.is_empty() {
                    return Err(WithWatchError::MissingExecCommand);
                }
                Ok(CommandMode::Exec {
                    inputs: exec.input.clone(),
                    argv: exec.command.clone(),
                })
            }
            (None, Some(Command::Passthrough(argv))) => {
                if argv.is_empty() {
                    return Err(WithWatchError::MissingCommand);
                }
                Ok(CommandMode::Passthrough { argv: argv.clone() })
            }
            (None, None) => Err(WithWatchError::MissingCommand),
        }
    }
}

#[cfg(test)]
mod tests {
    use clap::Parser;

    use super::{Cli, CommandMode};
    use crate::{error::WithWatchError, snapshot::ChangeDetectionMode};

    #[test]
    fn passthrough_mode_preserves_external_subcommand_arguments() {
        let cli = Cli::parse_from(["with-watch", "cp", "a", "b"]);
        let mode = cli.command_mode().expect("command mode");

        match mode {
            CommandMode::Passthrough { argv } => {
                assert_eq!(argv.len(), 3);
                assert_eq!(argv[0].to_string_lossy(), "cp");
                assert_eq!(argv[1].to_string_lossy(), "a");
                assert_eq!(argv[2].to_string_lossy(), "b");
            }
            other => panic!("unexpected mode: {other:?}"),
        }
    }

    #[test]
    fn shell_mode_is_mutually_exclusive_with_subcommands() {
        let error = Cli::parse_from(["with-watch", "--shell", "echo hi", "cp", "a", "b"])
            .command_mode()
            .expect_err("expected error");

        assert!(matches!(error, WithWatchError::ConflictingModes));
    }

    #[test]
    fn exec_mode_uses_mtime_only_when_hashing_is_disabled() {
        let cli = Cli::parse_from([
            "with-watch",
            "--no-hash",
            "exec",
            "--input",
            "src/**/*.rs",
            "--",
            "cargo",
            "test",
        ]);

        assert_eq!(cli.change_detection_mode(), ChangeDetectionMode::MtimeOnly);
    }

    #[test]
    fn short_help_stays_compact_while_long_help_includes_inventory() {
        let mut short_command = Cli::command_with_inventory();
        let mut short_help = Vec::new();
        short_command
            .write_help(&mut short_help)
            .expect("write short help");

        let mut long_command = Cli::command_with_inventory();
        let mut long_help = Vec::new();
        long_command
            .write_long_help(&mut long_help)
            .expect("write long help");

        let short_help = String::from_utf8(short_help).expect("short help utf8");
        let long_help = String::from_utf8(long_help).expect("long help utf8");

        assert!(!short_help.contains("Wrapper commands:"));
        assert!(!short_help.contains("Recognized but not auto-watchable commands:"));
        assert!(long_help.contains("Wrapper commands:"));
        assert!(long_help.contains("Recognized but not auto-watchable commands:"));
    }
}
