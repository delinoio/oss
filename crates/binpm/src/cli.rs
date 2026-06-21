use std::ffi::{OsStr, OsString};

use clap::{Args, CommandFactory, Parser, Subcommand, ValueEnum};

use crate::contract::Scope;

#[derive(Debug, Parser)]
#[command(
    name = "binpm",
    version,
    about = "Install and run native command-line tools from release assets"
)]
pub struct Cli {
    #[command(subcommand)]
    pub command: Command,
}

impl Cli {
    pub fn parse_args() -> Self {
        Self::parse()
    }

    pub fn command_for_tests() -> clap::Command {
        <Self as CommandFactory>::command()
    }
}

#[derive(Debug, Subcommand)]
pub enum Command {
    /// Sync local tools or install a source globally.
    Install(InstallArgs),
    /// Declare a local tool and install it into the project bin directory.
    Add(AddArgs),
    /// Run a local manifest command or a command from an explicit package.
    #[command(name = "x")]
    Exec(ExecArgs),
    /// Inspect and manage the global release asset cache.
    Cache(CacheArgs),
    /// List declared and installed tools.
    List(ScopedArgs),
    /// Remove a tool from the selected scope.
    Remove(RemoveArgs),
    /// Show source and selected asset metadata.
    Info(InfoArgs),
    /// Compare selected tools with latest stable releases.
    Outdated(ScopedArgs),
    /// Update selected tools.
    Update(UpdateArgs),
    /// Inspect local and global binpm state.
    Doctor,
    /// Explain source, release, target, asset, binary, and verification
    /// decisions.
    Explain(ExplainArgs),
    /// Verify lockfile, package records, cache bytes, and installed
    /// executables.
    Verify(VerifyArgs),
    /// Create a minimal local binpm.toml manifest.
    Init(InitArgs),
    /// Print shell commands for adding binpm bin directories to PATH.
    Env(EnvArgs),
}

#[derive(Debug, Clone, Args)]
pub struct InstallArgs {
    /// Optional source spec. Omit to sync the local binpm.toml manifest.
    pub source: Option<String>,

    #[command(flatten)]
    pub scope: ScopeArgs,

    #[command(flatten)]
    pub lockfile: LockfileArgs,

    /// Fail unless upstream digest, checksum, or verified signature material is
    /// available.
    #[arg(long)]
    pub require_verified: bool,

    /// Bypass future confirmation prompts for scripting.
    #[arg(long)]
    pub no_confirm: bool,
}

#[derive(Debug, Clone, Args)]
pub struct AddArgs {
    pub cmd: String,
    pub source: String,

    #[command(flatten)]
    pub lockfile: LockfileArgs,

    /// Fail unless upstream digest, checksum, or verified signature material is
    /// available.
    #[arg(long)]
    pub require_verified: bool,

    /// Bypass future confirmation prompts for scripting.
    #[arg(long)]
    pub no_confirm: bool,
}

#[derive(Debug, Clone, Args)]
pub struct ExecArgs {
    /// Explicit package source for one-off execution.
    #[arg(long, value_name = "SOURCE")]
    pub package: Option<String>,

    #[command(flatten)]
    pub lockfile: LockfileArgs,

    /// Command to execute followed by arguments forwarded to it.
    #[arg(required = true, trailing_var_arg = true, allow_hyphen_values = true)]
    pub command: Vec<OsString>,
}

impl ExecArgs {
    pub fn cmd(&self) -> &OsStr {
        self.command
            .first()
            .expect("clap requires at least one x command argument")
            .as_os_str()
    }

    pub fn args(&self) -> &[OsString] {
        match self.command.get(1) {
            Some(separator) if separator == OsStr::new("--") => &self.command[2..],
            _ => &self.command[1..],
        }
    }
}

#[derive(Debug, Clone, Args)]
pub struct CacheArgs {
    #[command(subcommand)]
    pub command: CacheCommand,
}

#[derive(Debug, Clone, Subcommand)]
pub enum CacheCommand {
    /// List cache entries.
    List,
    /// Remove only cache entries not referenced by installed package records.
    Prune {
        /// Bypass future confirmation prompts for scripting.
        #[arg(long)]
        no_confirm: bool,
    },
    /// Remove all cache entries while preserving installed package records and
    /// bins.
    Clean {
        /// Bypass future confirmation prompts for scripting.
        #[arg(long)]
        no_confirm: bool,
    },
    /// Print a read-only CI cache key for the current target and lockfile.
    Key,
}

#[derive(Debug, Clone, Args)]
pub struct ScopedArgs {
    #[command(flatten)]
    pub scope: ScopeArgs,
}

#[derive(Debug, Clone, Args)]
pub struct RemoveArgs {
    pub cmd: String,

    #[command(flatten)]
    pub scope: ScopeArgs,

    /// Show the selected scope and planned removal without mutating state.
    #[arg(long)]
    pub dry_run: bool,

    /// Bypass future confirmation prompts for scripting.
    #[arg(long)]
    pub no_confirm: bool,
}

#[derive(Debug, Clone, Args)]
pub struct InfoArgs {
    pub cmd_or_source: String,

    #[command(flatten)]
    pub scope: ScopeArgs,
}

#[derive(Debug, Clone, Args)]
pub struct UpdateArgs {
    pub cmd: Vec<String>,

    #[command(flatten)]
    pub scope: ScopeArgs,

    #[command(flatten)]
    pub lockfile: LockfileArgs,

    /// Fail unless upstream digest, checksum, or verified signature material is
    /// available.
    #[arg(long)]
    pub require_verified: bool,

    /// Show the selected scope and planned updates without mutating state.
    #[arg(long)]
    pub dry_run: bool,

    /// Bypass future confirmation prompts for scripting.
    #[arg(long)]
    pub no_confirm: bool,
}

#[derive(Debug, Clone, Args)]
pub struct ExplainArgs {
    pub cmd_or_source: String,

    #[command(flatten)]
    pub scope: ScopeArgs,
}

#[derive(Debug, Clone, Args)]
pub struct VerifyArgs {
    #[command(flatten)]
    pub scope: ScopeArgs,

    /// Fail unless upstream digest, checksum, or verified signature material is
    /// available.
    #[arg(long)]
    pub require_verified: bool,
}

#[derive(Debug, Clone, Args)]
pub struct InitArgs {
    /// Replace an existing binpm.toml.
    #[arg(long)]
    pub force: bool,
}

#[derive(Debug, Clone, Args)]
pub struct EnvArgs {
    #[arg(long, value_enum, ignore_case = true)]
    pub shell: Shell,
}

#[derive(Debug, Clone, Args)]
pub struct ScopeArgs {
    /// Force project-local scope.
    #[arg(long, conflicts_with = "global")]
    pub local: bool,

    /// Force user-global scope.
    #[arg(long, conflicts_with = "local")]
    pub global: bool,
}

impl ScopeArgs {
    pub fn scope(&self) -> Scope {
        match (self.local, self.global) {
            (true, false) => Scope::Local,
            (false, true) => Scope::Global,
            _ => Scope::Auto,
        }
    }
}

#[derive(Debug, Clone, Args)]
pub struct LockfileArgs {
    /// Fail if binpm.lock would need to be created or modified.
    #[arg(long, conflicts_with = "no_frozen_lockfile")]
    pub frozen_lockfile: bool,

    /// Allow binpm.lock changes even when CI=true.
    #[arg(long)]
    pub no_frozen_lockfile: bool,
}

impl LockfileArgs {
    pub fn frozen_lockfile(&self) -> bool {
        if self.no_frozen_lockfile {
            return false;
        }
        self.frozen_lockfile || std::env::var("CI").is_ok_and(|value| value == "true")
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
#[value(rename_all = "lower")]
pub enum Shell {
    Bash,
    Zsh,
    Fish,
    #[value(alias = "pwsh")]
    Powershell,
    Cmd,
}

impl Shell {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Bash => "bash",
            Self::Zsh => "zsh",
            Self::Fish => "fish",
            Self::Powershell => "powershell",
            Self::Cmd => "cmd",
        }
    }
}

#[cfg(test)]
mod tests {
    use std::ffi::{OsStr, OsString};

    use clap::Parser;

    use super::{CacheCommand, Cli, Command, Shell};
    use crate::contract::Scope;

    #[test]
    fn command_surface_includes_stable_subcommands() {
        let mut command = Cli::command_for_tests();
        let help = command.render_long_help().to_string();

        for expected in [
            "install", "add", "x", "cache", "list", "remove", "info", "outdated", "update",
            "doctor", "explain", "verify", "init", "env",
        ] {
            assert!(
                help.contains(expected),
                "missing `{expected}` in help:\n{help}"
            );
        }
    }

    #[test]
    fn parses_cache_key_command() {
        let cli = Cli::parse_from(["binpm", "cache", "key"]);

        match cli.command {
            Command::Cache(cache) => assert!(matches!(cache.command, CacheCommand::Key)),
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_x_command_with_forwarded_args() {
        let cli = Cli::parse_from([
            "binpm",
            "x",
            "--package",
            "github:BurntSushi/ripgrep",
            "rg",
            "--",
            "--files",
            "-g",
            "*.rs",
        ]);

        match cli.command {
            Command::Exec(exec) => {
                assert_eq!(exec.package.as_deref(), Some("github:BurntSushi/ripgrep"));
                assert_eq!(exec.cmd(), OsStr::new("rg"));
                assert_eq!(
                    exec.args(),
                    vec![
                        OsString::from("--files"),
                        OsString::from("-g"),
                        OsString::from("*.rs")
                    ]
                );
            }
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_x_command_help_as_forwarded_arg() {
        let cli = Cli::try_parse_from(["binpm", "x", "rg", "--help"]).expect("parse x command");

        match cli.command {
            Command::Exec(exec) => {
                assert_eq!(exec.cmd(), OsStr::new("rg"));
                assert_eq!(exec.args(), vec![OsString::from("--help")]);
            }
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_x_command_package_flag_after_cmd_as_forwarded_arg() {
        let cli = Cli::try_parse_from([
            "binpm",
            "x",
            "--package",
            "github:BurntSushi/ripgrep",
            "rg",
            "--package",
            "literal",
        ])
        .expect("parse x command");

        match cli.command {
            Command::Exec(exec) => {
                assert_eq!(exec.package.as_deref(), Some("github:BurntSushi/ripgrep"));
                assert_eq!(exec.cmd(), OsStr::new("rg"));
                assert_eq!(
                    exec.args(),
                    vec![OsString::from("--package"), OsString::from("literal")]
                );
            }
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn scoped_args_default_to_auto() {
        let cli = Cli::parse_from(["binpm", "list"]);

        match cli.command {
            Command::List(args) => assert_eq!(args.scope.scope(), Scope::Auto),
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_env_shell() {
        let cli = Cli::parse_from(["binpm", "env", "--shell", "fish"]);

        match cli.command {
            Command::Env(args) => assert_eq!(args.shell, Shell::Fish),
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_env_powershell_case_insensitively() {
        let cli = Cli::parse_from(["binpm", "env", "--shell", "PowerShell"]);

        match cli.command {
            Command::Env(args) => assert_eq!(args.shell, Shell::Powershell),
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_env_cmd_as_deferred_shell_value() {
        let cli = Cli::parse_from(["binpm", "env", "--shell", "cmd"]);

        match cli.command {
            Command::Env(args) => assert_eq!(args.shell, Shell::Cmd),
            other => panic!("unexpected command: {other:?}"),
        }
    }
}
