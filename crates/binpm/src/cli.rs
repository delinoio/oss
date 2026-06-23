use std::{
    ffi::{OsStr, OsString},
    path::PathBuf,
};

use clap::{Args, CommandFactory, Parser, Subcommand, ValueEnum};

use crate::contract::Scope;

#[derive(Debug, Parser)]
#[command(
    name = "binpm",
    version,
    about = "Install and run native command-line tools from release assets"
)]
pub struct Cli {
    /// Emit stable JSON for diagnostics and cache cleanup summaries.
    #[arg(long, global = true)]
    pub json: bool,

    /// Enable info-level binpm tracing diagnostics.
    #[arg(short = 'v', long, global = true, conflicts_with = "debug")]
    pub verbose: bool,

    /// Enable debug-level binpm tracing diagnostics.
    #[arg(long, global = true)]
    pub debug: bool,

    #[command(subcommand)]
    pub command: Command,
}

impl Cli {
    pub fn parse_args() -> Self {
        Self::parse()
    }

    pub fn try_parse_args() -> std::result::Result<Self, clap::Error> {
        Self::try_parse()
    }

    pub fn json_requested<I, T>(args: I) -> bool
    where
        I: IntoIterator<Item = T>,
        T: AsRef<OsStr>,
    {
        args.into_iter()
            .any(|arg| arg.as_ref() == OsStr::new("--json"))
    }

    pub fn command_for_tests() -> clap::Command {
        <Self as CommandFactory>::command()
    }

    pub fn log_verbosity(&self) -> LogVerbosity {
        if self.debug {
            LogVerbosity::Debug
        } else if self.verbose {
            LogVerbosity::Verbose
        } else {
            LogVerbosity::Default
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LogVerbosity {
    Default,
    Verbose,
    Debug,
}

#[derive(Debug, Subcommand)]
pub enum Command {
    /// Sync local tools, or install a source globally.
    Install(InstallArgs),
    /// Declare a local tool and install it into the project bin directory.
    Add(AddArgs),
    /// Execute a local manifest command or one-off package command.
    #[command(name = "x", visible_aliases = ["exec", "run"])]
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
    /// Update selected local or global tools, or all tools when none are named.
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
#[command(
    after_help = "Supported forms:\n  binpm install\n      Sync the local binpm.toml manifest.\n  \
                  binpm install <source> [--as <cmd>] [--bin <upstream-binary>]\n      Install a \
                  source globally, even inside a project.\n\nUse `binpm add <cmd> <source>` for \
                  project-local tools. `binpm install <source> --local` is not supported."
)]
pub struct InstallArgs {
    /// Source spec for a global install. Omit to sync the local binpm.toml
    /// manifest.
    pub source: Option<String>,

    /// Command name to expose for a global source install.
    #[arg(long = "as", value_name = "CMD", requires = "source")]
    pub alias: Option<String>,

    /// Upstream executable name or archive member path to install for a
    /// global source install.
    #[arg(long, value_name = "BIN", requires = "source")]
    pub bin: Option<String>,

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

    /// Upstream executable name or archive member path to install for this
    /// local command.
    #[arg(long, value_name = "BIN")]
    pub bin: Option<String>,

    /// Additional local command declaration using the same source. Use
    /// CMD=BIN to expose CMD from an upstream binary or archive member.
    #[arg(long = "also", value_name = "CMD=BIN")]
    pub also: Vec<String>,

    #[command(flatten)]
    pub lockfile: LockfileArgs,

    /// Update only binpm.toml; do not resolve, install, or write binpm.lock.
    #[arg(long, alias = "no-install")]
    pub manifest_only: bool,

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

    /// Upstream executable name or archive member path to run from the explicit
    /// package.
    #[arg(long, value_name = "BIN", requires = "package")]
    pub bin: Option<String>,

    #[command(flatten)]
    pub lockfile: LockfileArgs,

    /// Command to execute followed by arguments forwarded to it. May be
    /// omitted with --package for the safe one-off package shortcut.
    #[arg(trailing_var_arg = true, allow_hyphen_values = true)]
    pub command: Vec<OsString>,
}

impl ExecArgs {
    pub fn cmd(&self) -> Option<&OsStr> {
        match self.command.first() {
            Some(separator) if separator == OsStr::new("--") => None,
            Some(cmd) => Some(cmd.as_os_str()),
            None => None,
        }
    }

    pub fn args(&self) -> &[OsString] {
        if self.command.is_empty() {
            return &[];
        }
        if self.command.first() == Some(&OsString::from("--")) {
            return &self.command[1..];
        }
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
    /// Remove stale project refs, then cache entries not referenced by records
    /// or refs.
    Prune {
        /// Bypass future confirmation prompts for scripting.
        #[arg(long)]
        no_confirm: bool,
    },
    /// Remove cache asset entries while preserving refs, package records, and
    /// bins.
    Clean {
        /// Bypass future confirmation prompts for scripting.
        #[arg(long)]
        no_confirm: bool,
    },
    /// Print a read-only CI cache key and lockfile status.
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
    /// Tool commands to update. Omit to update every tool in the selected
    /// scope; use --dry-run to preview that broader update.
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
    /// Create binpm.toml at this explicit destination instead of the inferred
    /// project root. Existing files are never overwritten.
    #[arg(long, value_name = "PATH")]
    pub manifest_path: Option<PathBuf>,
}

#[derive(Debug, Clone, Args)]
pub struct EnvArgs {
    /// Shell syntax to render. Omit to infer from SHELL or ComSpec.
    #[arg(long, value_enum, ignore_case = true)]
    pub shell: Option<Shell>,

    /// Print only the global bin PATH command for explicit profile setup.
    #[arg(long, conflicts_with = "local")]
    pub global: bool,

    /// Print only the project-local bin PATH command for this project/session.
    #[arg(long)]
    pub local: bool,
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

    pub fn frozen_lockfile_mode(&self) -> Option<&'static str> {
        if self.no_frozen_lockfile {
            return None;
        }
        if self.frozen_lockfile {
            Some("--frozen-lockfile")
        } else if std::env::var("CI").is_ok_and(|value| value == "true") {
            Some("CI=true")
        } else {
            None
        }
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
            "install",
            "add",
            "x",
            "exec",
            "run",
            "Execute a local manifest command or one-off package command",
            "cache",
            "list",
            "remove",
            "info",
            "outdated",
            "update",
            "doctor",
            "explain",
            "verify",
            "init",
            "env",
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
                assert_eq!(exec.cmd(), Some(OsStr::new("rg")));
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
                assert_eq!(exec.cmd(), Some(OsStr::new("rg")));
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
                assert_eq!(exec.cmd(), Some(OsStr::new("rg")));
                assert_eq!(
                    exec.args(),
                    vec![OsString::from("--package"), OsString::from("literal")]
                );
            }
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_execution_aliases_with_same_forwarding_and_package_flags() {
        for alias in ["exec", "run"] {
            let cli = Cli::try_parse_from([
                "binpm",
                alias,
                "--no-frozen-lockfile",
                "--package",
                "github:BurntSushi/ripgrep",
                "rg",
                "--",
                "--package",
                "literal",
            ])
            .unwrap_or_else(|error| panic!("parse {alias} alias: {error}"));

            match cli.command {
                Command::Exec(exec) => {
                    assert_eq!(exec.package.as_deref(), Some("github:BurntSushi/ripgrep"));
                    assert!(exec.lockfile.no_frozen_lockfile);
                    assert_eq!(exec.cmd(), Some(OsStr::new("rg")));
                    assert_eq!(
                        exec.args(),
                        vec![OsString::from("--package"), OsString::from("literal")]
                    );
                }
                other => panic!("unexpected command for {alias}: {other:?}"),
            }
        }
    }

    #[test]
    fn parses_global_install_alias_and_binary_selection() {
        let cli = Cli::parse_from([
            "binpm",
            "install",
            "github:owner/repo",
            "--as",
            "tool",
            "--bin",
            "bin/tool",
        ]);

        match cli.command {
            Command::Install(args) => {
                assert_eq!(args.source.as_deref(), Some("github:owner/repo"));
                assert_eq!(args.alias.as_deref(), Some("tool"));
                assert_eq!(args.bin.as_deref(), Some("bin/tool"));
            }
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_manifest_only_add_with_additional_declarations() {
        let cli = Cli::parse_from([
            "binpm",
            "add",
            "foo",
            "github:owner/tools",
            "--bin",
            "bin/foo",
            "--also",
            "bar=bin/bar",
            "--manifest-only",
        ]);

        match cli.command {
            Command::Add(args) => {
                assert_eq!(args.cmd, "foo");
                assert_eq!(args.bin.as_deref(), Some("bin/foo"));
                assert_eq!(args.also, vec!["bar=bin/bar"]);
                assert!(args.manifest_only);
            }
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_package_shortcut_without_command() {
        let cli = Cli::parse_from(["binpm", "x", "--package", "github:owner/tool"]);

        match cli.command {
            Command::Exec(exec) => {
                assert_eq!(exec.package.as_deref(), Some("github:owner/tool"));
                assert_eq!(exec.cmd(), None);
                assert!(exec.args().is_empty());
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
            Command::Env(args) => assert_eq!(args.shell, Some(Shell::Fish)),
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_env_powershell_case_insensitively() {
        let cli = Cli::parse_from(["binpm", "env", "--shell", "PowerShell"]);

        match cli.command {
            Command::Env(args) => assert_eq!(args.shell, Some(Shell::Powershell)),
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_env_pwsh_alias_as_powershell() {
        let cli = Cli::parse_from(["binpm", "env", "--shell", "pwsh"]);

        match cli.command {
            Command::Env(args) => assert_eq!(args.shell, Some(Shell::Powershell)),
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_env_without_shell_for_runtime_inference() {
        let cli = Cli::parse_from(["binpm", "env", "--global"]);

        match cli.command {
            Command::Env(args) => {
                assert_eq!(args.shell, None);
                assert!(args.global);
            }
            other => panic!("unexpected command: {other:?}"),
        }
    }

    #[test]
    fn parses_env_cmd_as_deferred_shell_value() {
        let cli = Cli::parse_from(["binpm", "env", "--shell", "cmd"]);

        match cli.command {
            Command::Env(args) => assert_eq!(args.shell, Some(Shell::Cmd)),
            other => panic!("unexpected command: {other:?}"),
        }
    }
}
