use clap::{Parser, Subcommand, ValueEnum};

#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum OutputFormat {
    Human,
    Json,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum OutputColorMode {
    Auto,
    Always,
    Never,
}

impl OutputColorMode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Auto => "auto",
            Self::Always => "always",
            Self::Never => "never",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ToolchainListDetail {
    Standard,
    Quiet,
    Verbose,
}

impl ToolchainListDetail {
    pub fn from_flags(quiet: bool, verbose: bool) -> Self {
        if quiet {
            Self::Quiet
        } else if verbose {
            Self::Verbose
        } else {
            Self::Standard
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::Standard => "standard",
            Self::Quiet => "quiet",
            Self::Verbose => "verbose",
        }
    }
}

#[derive(Debug, Parser)]
#[command(
    name = "nodeup",
    version,
    about = "Rustup-like Node.js version manager",
    after_help = "Script-safe output:\n  Use `--output json` for structured automation.\n  Use \
                  `nodeup toolchain list --quiet` for raw runtime identifiers.\n  Use `nodeup \
                  completions <shell> >file` for raw completion scripts.\n  Logs are written to \
                  stderr when enabled; JSON mode keeps Nodeup logging off by default for \
                  parseable automation output, as do quiet runtime lists and completion \
                  generation.\n\nColor controls:\n  --color accepts auto, always, or never.\n  \
                  NODEUP_COLOR accepts auto, always, or never for human stdout/stderr.\n  \
                  NODEUP_LOG_COLOR accepts auto, always, or never for logs.\n  Precedence for \
                  human output is --color > NODEUP_COLOR > NO_COLOR > stream-aware auto.\n  Run \
                  `nodeup show color` to inspect ignored invalid values and NO_COLOR conflicts."
)]
pub struct Cli {
    /// Output format for command results. Use `json` for structured automation;
    /// JSON mode keeps Nodeup logging off for parseable automation output.
    #[arg(long, global = true, value_enum, default_value_t = OutputFormat::Human)]
    pub output: OutputFormat,

    /// Color mode for human output (`auto`, `always`, or `never`). Overrides
    /// NODEUP_COLOR and NO_COLOR.
    #[arg(long, global = true, value_enum)]
    pub color: Option<OutputColorMode>,

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
    /// Update channels/tracked selectors; exact versions are immutable pins.
    Update {
        /// Runtime selectors to update. Exact versions are skipped as immutable
        /// pins; install or select a newer exact runtime with `toolchain
        /// install`, `default`, or `override set`.
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
    /// Manage executable-name dispatch shims.
    Shim {
        #[command(subcommand)]
        command: ShimCommand,
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
        shell: String,
        /// Optional top-level command scope for completion generation.
        #[arg(value_name = "COMMAND", allow_hyphen_values = true)]
        command: Vec<String>,
    },
}

#[derive(Debug, Subcommand)]
pub enum ToolchainCommand {
    /// List installed runtimes.
    List {
        /// Print compact runtime identifiers only.
        #[arg(long, conflicts_with = "verbose")]
        quiet: bool,
        /// Include runtime metadata such as resolved target paths.
        #[arg(long, conflicts_with = "quiet")]
        verbose: bool,
    },
    /// Install one or more runtimes.
    Install {
        /// Runtime selectors to install.
        #[arg(required = true)]
        runtimes: Vec<String>,
    },
    /// Uninstall one or more runtimes.
    Uninstall {
        /// Installed runtime selectors to remove.
        #[arg(required = true)]
        runtimes: Vec<String>,
    },
    /// Link an existing local runtime directory.
    Link {
        /// Alias used to reference the linked runtime.
        name: String,
        /// Path to a runtime directory containing `bin/node` or `bin/node.exe`.
        path: String,
    },
    /// Remove a linked runtime record without deleting its external directory.
    Unlink {
        /// Linked runtime aliases to remove.
        names: Vec<String>,
    },
}

#[derive(Debug, Subcommand)]
pub enum ShowCommand {
    /// Show the currently selected runtime after resolution.
    #[command(name = "active-runtime")]
    ActiveRuntime,
    /// Show the nodeup home directory path.
    Home,
    /// Show effective human output and log color decisions, including ignored
    /// invalid color env values and NO_COLOR conflicts.
    Color,
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
        #[arg(long, conflicts_with = "nonexistent")]
        path: Option<String>,
        /// Remove stale entries whose directories no longer exist.
        #[arg(long, conflicts_with = "path")]
        nonexistent: bool,
    },
}

#[derive(Debug, Subcommand)]
pub enum ShimCommand {
    /// Create or repair managed shims for node, npm, npx, yarn, and pnpm.
    Setup {
        /// Directory where managed shims are created.
        #[arg(long)]
        dir: Option<String>,
    },
}

#[derive(Debug, Subcommand)]
pub enum SelfCommand {
    /// Update the nodeup binary.
    Update,
    /// Remove Nodeup-owned data, cache, and config. Binary, shims, and PATH
    /// remain manual.
    Uninstall,
    /// Migrate nodeup local data to the latest schema.
    #[command(name = "upgrade-data")]
    UpgradeData,
}
