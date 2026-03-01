use clap::{Parser, Subcommand, ValueEnum};

#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum OutputFormat {
    Human,
    Json,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum CompletionShell {
    Bash,
    Zsh,
    Fish,
}

impl CompletionShell {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Bash => "bash",
            Self::Zsh => "zsh",
            Self::Fish => "fish",
        }
    }
}

#[derive(Debug, Parser)]
#[command(
    name = "nodeup",
    version,
    about = "Rustup-like Node.js version manager"
)]
pub struct Cli {
    /// Output format for command results.
    #[arg(long, global = true, value_enum, default_value_t = OutputFormat::Human)]
    pub output: OutputFormat,

    #[command(subcommand)]
    pub command: Command,
}

#[derive(Debug, Subcommand)]
pub enum Command {
    /// Manage installed runtimes.
    Toolchain {
        #[command(subcommand)]
        command: ToolchainCommand,
    },
    /// Set or show the global default runtime.
    Default {
        /// Runtime selector such as `22.1.0`, `lts`, or `current`.
        runtime: Option<String>,
    },
    /// Show runtime resolution details and nodeup directories.
    Show {
        #[command(subcommand)]
        command: ShowCommand,
    },
    /// Update selected runtimes or tracked selectors.
    Update {
        /// Runtime selectors to update. If omitted, updates tracked selectors.
        runtimes: Vec<String>,
    },
    /// Check for available updates without installing them.
    Check,
    /// Manage directory-scoped runtime overrides.
    Override {
        #[command(subcommand)]
        command: OverrideCommand,
    },
    /// Print the resolved executable path for a command.
    Which {
        /// Use the provided runtime selector instead of override/default
        /// resolution.
        #[arg(long)]
        runtime: Option<String>,
        /// Executable name to resolve.
        command: String,
    },
    /// Run a command with a selected runtime.
    Run {
        /// Install the runtime first if it is missing.
        #[arg(long)]
        install: bool,
        /// Runtime selector used to execute the delegated command.
        runtime: String,
        /// Delegated command and arguments.
        #[arg(required = true, trailing_var_arg = true)]
        command: Vec<String>,
    },
    /// Manage the nodeup installation.
    #[command(name = "self")]
    SelfCmd {
        #[command(subcommand)]
        command: SelfCommand,
    },
    /// Generate shell completion scripts.
    Completions {
        /// Target shell (for example: `bash`, `zsh`, or `fish`).
        shell: CompletionShell,
        /// Optional command scope for completion generation.
        command: Option<String>,
    },
}

#[derive(Debug, Subcommand)]
pub enum ToolchainCommand {
    /// List installed runtimes.
    List,
    /// Install one or more runtimes.
    Install {
        /// Runtime selectors to install.
        runtimes: Vec<String>,
    },
    /// Uninstall one or more runtimes.
    Uninstall {
        /// Installed runtime selectors to remove.
        runtimes: Vec<String>,
    },
    /// Link an existing local runtime directory.
    Link {
        /// Alias used to reference the linked runtime.
        name: String,
        /// Path to a runtime directory containing `bin/node`.
        path: String,
    },
}

#[derive(Debug, Subcommand)]
pub enum ShowCommand {
    /// Show the currently selected runtime after resolution.
    #[command(name = "active-runtime")]
    ActiveRuntime,
    /// Show the nodeup home directory path.
    Home,
}

#[derive(Debug, Subcommand)]
pub enum OverrideCommand {
    /// List configured directory overrides.
    List,
    /// Set a runtime override for a directory.
    Set {
        /// Runtime selector to pin for the target directory.
        runtime: String,
        /// Override target directory. Defaults to current working directory.
        #[arg(long)]
        path: Option<String>,
    },
    /// Remove a runtime override for a directory.
    Unset {
        /// Override target directory. Defaults to current working directory.
        #[arg(long)]
        path: Option<String>,
        /// Remove stale entries whose directories no longer exist.
        #[arg(long)]
        nonexistent: bool,
    },
}

#[derive(Debug, Subcommand)]
pub enum SelfCommand {
    /// Update the nodeup binary.
    Update,
    /// Uninstall nodeup from the current machine.
    Uninstall,
    /// Migrate nodeup local data to the latest schema.
    #[command(name = "upgrade-data")]
    UpgradeData,
}
